package structure_websites

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"w100n_backend_core/core/db"
	"w100n_backend_core/core/db/utils"
)

// GetStructureWebsites loads all documents from the "structure_websites" collection
// and returns them as JSON.
func GetStructureWebsites(ctx context.Context) ([]map[string]interface{}, error) {
	mongoDB, err := utils.GetMongoDB(ctx)
	if err != nil {
		log.Printf("Failed to connect to MongoDB: %v", err)
		return nil, err
	}

	collection := mongoDB.Collection("structure_websites")
	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []map[string]interface{}
	for cursor.Next(ctx) {
		var doc map[string]interface{}
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	return docs, nil
}

// UpdateStructureWebsite replaces the entire JSON document for the provided id.
// доступно лише у режимі LOCAL_DEVELOPMENT=true, щоб уникнути випадкових змін на проді.
func UpdateStructureWebsite(ctx context.Context, id string, payload map[string]interface{}) (map[string]interface{}, error) {
	if strings.ToLower(os.Getenv("LOCAL_DEVELOPMENT")) != "true" {
		return nil, fmt.Errorf("structure_websites mutation is available only when LOCAL_DEVELOPMENT=true")
	}

	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	if payload == nil {
		return nil, fmt.Errorf("payload cannot be nil")
	}

	mongoDB, err := utils.GetMongoDB(ctx)
	if err != nil {
		log.Printf("Failed to connect to MongoDB: %v", err)
		return nil, err
	}

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid id format: %w", err)
	}

	collection := mongoDB.Collection("structure_websites")

	existingDoc := make(map[string]interface{})
	err = collection.FindOne(ctx, bson.M{"_id": objectID}).Decode(&existingDoc)
	if err != nil {
		if err != mongo.ErrNoDocuments {
			return nil, err
		}
		existingDoc = map[string]interface{}{"_id": objectID}
	}

	// Створюємо копію мапи, щоб не модифікувати початковий payload.
	doc := mergeJSONDocuments(existingDoc, payload)
	doc["_id"] = objectID

	opts := options.Replace().SetUpsert(true)
	result, err := collection.ReplaceOne(ctx, bson.M{"_id": objectID}, doc, opts)
	if err != nil {
		return nil, err
	}

	log.Printf(
		"[StructureWebsites] Updated document %s (matched: %d, modified: %d, upsertedID: %v)",
		id,
		result.MatchedCount,
		result.ModifiedCount,
		result.UpsertedID,
	)

	return doc, nil
}

func mergeJSONDocuments(base map[string]interface{}, updates map[string]interface{}) map[string]interface{} {
	if base == nil {
		base = make(map[string]interface{})
	}
	for k, v := range updates {
		if k == "_id" {
			continue
		}

		if vMap, ok := v.(map[string]interface{}); ok {
			baseMap, ok := base[k].(map[string]interface{})
			if !ok {
				baseMap = make(map[string]interface{})
			}
			base[k] = mergeJSONDocuments(baseMap, vMap)
			continue
		}

		base[k] = v
	}
	return base
}

type TemplateApplySummary struct {
	TemplateKey      string
	TargetField      string
	ClientNames      []string
	UpdatedDocuments int
	DeletedDocuments int
}

func ApplyStructureTemplate(ctx context.Context, templateKey string, documents []map[string]interface{}, targetField string) (*TemplateApplySummary, error) {
	if templateKey == "" {
		return nil, fmt.Errorf("templateKey is required")
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("documents payload cannot be empty")
	}

	sanitizedDocs := make([]map[string]interface{}, 0, len(documents))
	templateIDs := make([]primitive.ObjectID, 0, len(documents))

	for idx, rawDoc := range documents {
		if rawDoc == nil {
			continue
		}

		sanitized, docID, err := prepareTemplateDocument(rawDoc)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", idx, err)
		}
		sanitizedDocs = append(sanitizedDocs, sanitized)
		templateIDs = append(templateIDs, docID)
	}

	if len(sanitizedDocs) == 0 {
		return nil, fmt.Errorf("no usable documents after sanitizing input")
	}

	if targetField == "" {
		targetField = "structure_template"
	}

	if db.MongoClient == nil {
		return nil, fmt.Errorf("mongo client is not initialized")
	}

	coreDBName := os.Getenv("MONGO_DB_NAME")
	if coreDBName == "" {
		coreDBName = "core"
	}

	coreDatabase := db.MongoClient.Database(coreDBName)
	templatesColl := coreDatabase.Collection("structure_templates")
	clientsColl := coreDatabase.Collection("db_clients")

	effectiveField := targetField
	if targetField == "structure_template" {
		count, err := clientsColl.CountDocuments(ctx, bson.M{targetField: templateKey})
		if err != nil {
			return nil, fmt.Errorf("failed to count clients for template %q: %w", templateKey, err)
		}
		if count == 0 {
			effectiveField = "template"
		}
	}

	docsForStorage := make([]map[string]interface{}, len(sanitizedDocs))
	for i, doc := range sanitizedDocs {
		docsForStorage[i] = cloneMap(doc)
	}

	_, err := templatesColl.UpdateOne(
		ctx,
		bson.M{"template_key": templateKey},
		bson.M{"$set": bson.M{
			"template_key": templateKey,
			"documents":    docsForStorage,
			"updated_at":   time.Now(),
		}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert template definition: %w", err)
	}

	clientCursor, err := clientsColl.Find(ctx, bson.M{effectiveField: templateKey})
	if err != nil {
		return nil, fmt.Errorf("failed to query clients for template %q: %w", templateKey, err)
	}
	defer clientCursor.Close(ctx)

	summary := &TemplateApplySummary{
		TemplateKey: templateKey,
		TargetField: effectiveField,
	}

	for clientCursor.Next(ctx) {
		var clientDoc struct {
			ClientName string `bson:"client_name"`
		}
		if err := clientCursor.Decode(&clientDoc); err != nil {
			return nil, fmt.Errorf("failed to decode client document: %w", err)
		}

		clientName := strings.TrimSpace(clientDoc.ClientName)
		if clientName == "" {
			continue
		}

		clientDB := db.MongoClient.Database(clientName)
		collection := clientDB.Collection("structure_websites")

		for _, templateDoc := range sanitizedDocs {
			docCopy := cloneMap(templateDoc)
			_, err := collection.ReplaceOne(
				ctx,
				bson.M{"_id": docCopy["_id"]},
				docCopy,
				options.Replace().SetUpsert(true),
			)
			if err != nil {
				return nil, fmt.Errorf("client %s: failed to upsert document: %w", clientName, err)
			}
			summary.UpdatedDocuments++
		}

		deleteResult, err := collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$nin": templateIDs}})
		if err != nil {
			return nil, fmt.Errorf("client %s: failed to delete outdated documents: %w", clientName, err)
		}
		summary.DeletedDocuments += int(deleteResult.DeletedCount)
		summary.ClientNames = append(summary.ClientNames, clientName)
	}

	if err := clientCursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error while iterating clients: %w", err)
	}

	sort.Strings(summary.ClientNames)
	return summary, nil
}

func prepareTemplateDocument(doc map[string]interface{}) (map[string]interface{}, primitive.ObjectID, error) {
	sanitized := sanitizeTemplateDocument(doc)

	var id primitive.ObjectID
	if rawID, ok := doc["_id"]; ok {
		switch v := rawID.(type) {
		case string:
			v = strings.TrimSpace(v)
			if v == "" {
				id = primitive.NewObjectID()
			} else {
				objectID, err := primitive.ObjectIDFromHex(v)
				if err != nil {
					return nil, primitive.NilObjectID, fmt.Errorf("invalid _id value %q: %w", v, err)
				}
				id = objectID
			}
		case primitive.ObjectID:
			id = v
		default:
			return nil, primitive.NilObjectID, fmt.Errorf("_id must be a hex string or ObjectID, got %T", rawID)
		}
	} else {
		id = primitive.NewObjectID()
	}

	sanitized["_id"] = id
	return sanitized, id, nil
}
