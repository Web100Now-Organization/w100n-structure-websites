package functions

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// FetchGoogleReviewsJSON зчитує документи з колекції google_reviews,
// відфільтровує відгуки з status == true та не показує ті, у яких поле "text" пусте,
// і повертає результат у вигляді map.
func FetchGoogleReviewsJSON(ctx context.Context, db *mongo.Database) (map[string][]map[string]interface{}, error) {
	collections := []string{
		"google_reviews",
	}

	results := make(map[string][]map[string]interface{})

	for _, coll := range collections {
		docs, err := fetchAllDocuments(ctx, db, coll)
		if err != nil {
			// Якщо документів немає – записуємо порожній зріз
			if err == mongo.ErrNoDocuments {
				results[coll] = []map[string]interface{}{}
			} else {
				return nil, fmt.Errorf("failed to fetch %s: %w", coll, err)
			}
		} else {
			// Для колекції google_reviews – фільтруємо відгуки з status == true
			// і не включаємо ті, у яких поле "text" пусте.
			if coll == "google_reviews" {
				for i, doc := range docs {
					// Обробляємо як primitive.A, так і []interface{}
					if reviews, ok := doc["reviews"].(primitive.A); ok {
						var filteredReviews []interface{}
						for _, review := range reviews {
							if reviewMap, ok := review.(map[string]interface{}); ok {
								if status, ok := reviewMap["status"].(bool); ok && status {
									if text, ok := reviewMap["text"].(string); ok && text != "" {
										filteredReviews = append(filteredReviews, reviewMap)
									}
								}
							}
						}
						docs[i]["reviews"] = filteredReviews
					} else if reviews, ok := doc["reviews"].([]interface{}); ok {
						var filteredReviews []interface{}
						for _, review := range reviews {
							if reviewMap, ok := review.(map[string]interface{}); ok {
								if status, ok := reviewMap["status"].(bool); ok && status {
									if text, ok := reviewMap["text"].(string); ok && text != "" {
										filteredReviews = append(filteredReviews, reviewMap)
									}
								}
							}
						}
						docs[i]["reviews"] = filteredReviews
					}
				}
			}
			results[coll] = docs
		}
	}

	return results, nil
}

// fetchAllDocuments зчитує всі документи з вказаної колекції.
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
