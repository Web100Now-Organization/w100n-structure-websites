package functions

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// FetchSEOJSON reads documents from the structure_seo collection and returns them as a map.
func FetchSEOJSON(ctx context.Context, db *mongo.Database) (map[string][]map[string]interface{}, error) {
	collections := []string{
		"structure_seo",
	}

	results := make(map[string][]map[string]interface{})

	for _, coll := range collections {
		docs, err := fetchAllDocuments(ctx, db, coll)
		if err != nil {
			// If no documents found, write empty slice
			if err == mongo.ErrNoDocuments {
				results[coll] = []map[string]interface{}{}
			} else {
				return nil, fmt.Errorf("failed to fetch %s: %w", coll, err)
			}
		} else {
			results[coll] = docs
		}
	}

	return results, nil
}

// fetchAllDocuments reads all documents from the specified collection.
func fetchAllDocuments(ctx context.Context, db *mongo.Database, collectionName string) ([]map[string]interface{}, error) {
	collection := db.Collection(collectionName)

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var documents []map[string]interface{}
	for cursor.Next(ctx) {
		var doc map[string]interface{}
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		documents = append(documents, doc)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	if len(documents) == 0 {
		return nil, mongo.ErrNoDocuments
	}

	return documents, nil
}
