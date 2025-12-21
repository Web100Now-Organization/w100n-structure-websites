package structure_websites

import (
	"context"
	"fmt"
	"strings"

	"web100now-clients-platform/app/graph/model"
	"web100now-clients-platform/core"
	"web100now-clients-platform/core/db/utils"
	"web100now-clients-platform/core/logger"

	"go.mongodb.org/mongo-driver/bson"
)

// Resolver is the resolver for the structure_websites plugin.
type Resolver struct{}

// NewResolver creates a new instance of the structure_websites resolver.
func NewResolver() *Resolver {
	return &Resolver{}
}

// StructureWebsites loads all documents from the "structure_websites" collection
// and returns them as JSON.
func (r *Resolver) StructureWebsites(ctx context.Context) ([]core.JSON, error) {
	logger.LogInfo("[StructureWebsites] StructureWebsites called - Fetching all documents")

	mongoDB, err := utils.GetMongoDB(ctx)
	if err != nil {
		logger.LogError("[StructureWebsites] Failed to connect to MongoDB", err)
		return nil, err
	}

	logger.LogInfo("[StructureWebsites] MongoDB connection established successfully")

	collection := mongoDB.Collection("structure_websites")
	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		logger.LogError("[StructureWebsites] Failed to query structure_websites collection", err)
		return nil, err
	}
	defer cursor.Close(ctx)

	var result []core.JSON
	docCount := 0
	for cursor.Next(ctx) {
		var doc map[string]interface{}
		if err := cursor.Decode(&doc); err != nil {
			logger.LogError("[StructureWebsites] Failed to decode document", err)
			continue
		}
		result = append(result, core.JSON(doc))
		docCount++
	}

	if err := cursor.Err(); err != nil {
		logger.LogError("[StructureWebsites] Cursor iteration error", err)
		return nil, err
	}

	logger.LogInfo(fmt.Sprintf("[StructureWebsites] Successfully loaded %d document(s) from structure_websites", docCount))

	return result, nil
}

// UpdateStructureWebsite дозволяє повністю перезаписати документ (лише у LOCAL_DEVELOPMENT режимі).
func (r *Resolver) UpdateStructureWebsite(ctx context.Context, id string, payload core.JSON) (core.JSON, error) {
	logger.LogInfo(fmt.Sprintf("[StructureWebsites] UpdateStructureWebsite called - ID: %s", id))

	updatedDoc, err := UpdateStructureWebsite(ctx, id, map[string]interface{}(payload))
	if err != nil {
		logger.LogError(fmt.Sprintf("[StructureWebsites] Failed to update document %s", id), err)
		return nil, err
	}

	logger.LogInfo(fmt.Sprintf("[StructureWebsites] Successfully updated document %s", id))

	return core.JSON(updatedDoc), nil
}

func (r *Resolver) ApplyStructureTemplate(ctx context.Context, input model.StructureTemplateInput) (*model.ApplyStructureTemplateResult, error) {
	logger.LogInfo(fmt.Sprintf("[StructureWebsites] ApplyStructureTemplate called - Template: %s", input.TemplateKey))

	documents := make([]map[string]interface{}, 0, len(input.Documents))
	for _, doc := range input.Documents {
		if doc == nil {
			continue
		}
		documents = append(documents, map[string]interface{}(doc))
	}

	var targetField string
	if input.TargetField != nil {
		targetField = strings.TrimSpace(*input.TargetField)
	}

	summary, err := ApplyStructureTemplate(ctx, input.TemplateKey, documents, targetField)
	if err != nil {
		logger.LogError("[StructureWebsites] ApplyStructureTemplate failed", err)
		return nil, err
	}

	message := fmt.Sprintf(
		"Template %s applied via field %s to %d client(s); updated %d document(s), deleted %d document(s)",
		summary.TemplateKey,
		summary.TargetField,
		len(summary.ClientNames),
		summary.UpdatedDocuments,
		summary.DeletedDocuments,
	)

	logger.LogInfo(fmt.Sprintf("[StructureWebsites] %s", message))

	return &model.ApplyStructureTemplateResult{
		TemplateKey:      summary.TemplateKey,
		TargetField:      summary.TargetField,
		AffectedClients:  summary.ClientNames,
		UpdatedDocuments: summary.UpdatedDocuments,
		DeletedDocuments: summary.DeletedDocuments,
		Message:          message,
	}, nil
}
