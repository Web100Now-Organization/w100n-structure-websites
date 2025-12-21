package google_reviews

import (
	"context"
	"fmt"

	"web100now-clients-platform/app/graph/model"
	"web100now-clients-platform/app/plugins/w100n_structure_websites/sub_plugins/w100n_google_reviews/functions"
	"web100now-clients-platform/core/db/utils"
	"web100now-clients-platform/core/logger"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Resolver – резолвер для Google Reviews.
type Resolver struct{}

// NewResolver створює новий екземпляр резолвера для Google Reviews.
func NewResolver() *Resolver {
	return &Resolver{}
}

// GoogleReviews – метод резолвера для отримання даних Google Reviews.
func (r *Resolver) GoogleReviews(ctx context.Context) (*model.GoogleReviewsResponse, error) {
	logger.LogInfo("[GoogleReviews] GoogleReviews called - Fetching reviews from database")

	db, err := utils.GetMongoDB(ctx)
	if err != nil {
		logger.LogError("[GoogleReviews] Failed to connect to database", err)
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	logger.LogInfo("[GoogleReviews] MongoDB connection established successfully")

	result, err := functions.FetchGoogleReviewsJSON(ctx, db)
	if err != nil {
		logger.LogError("[GoogleReviews] Error fetching google reviews data", err)
		return nil, fmt.Errorf("error fetching google reviews: %w", err)
	}

	docs, ok := result["google_reviews"]
	if !ok {
		logger.LogError("[GoogleReviews] google_reviews key not found in result", fmt.Errorf("key missing"))
		return nil, fmt.Errorf("google_reviews key not found in result")
	}

	logger.LogInfo(fmt.Sprintf("[GoogleReviews] Found %d review document(s) in database", len(docs)))

	var reviews []*model.GoogleReview
	for i, doc := range docs {
		review, err := convertDocToGoogleReview(doc)
		if err != nil {
			logger.LogError(fmt.Sprintf("[GoogleReviews] Failed to convert document %d to GoogleReview", i), err)
			return nil, err
		}
		reviews = append(reviews, review)
	}

	logger.LogInfo(fmt.Sprintf("[GoogleReviews] Successfully converted %d review(s) to GraphQL model", len(reviews)))

	return &model.GoogleReviewsResponse{
		Reviews: reviews,
	}, nil
}

// convertDocToGoogleReview конвертує документ із MongoDB у GraphQL-модель GoogleReview.
func convertDocToGoogleReview(doc map[string]interface{}) (*model.GoogleReview, error) {
	var oid primitive.ObjectID
	// Спробуємо отримати _id як primitive.ObjectID
	if idVal, ok := doc["_id"].(primitive.ObjectID); ok {
		oid = idVal
	} else if str, ok := doc["_id"].(string); ok {
		var err error
		oid, err = primitive.ObjectIDFromHex(str)
		if err != nil {
			return nil, fmt.Errorf("failed to convert _id to ObjectID: %w", err)
		}
	} else {
		return nil, fmt.Errorf("unexpected type for _id")
	}

	name, _ := doc["name"].(string)

	var rating int
	if r, ok := doc["rating"].(int32); ok {
		rating = int(r)
	} else if r, ok := doc["rating"].(int); ok {
		rating = r
	} else if r, ok := doc["rating"].(float64); ok {
		rating = int(r)
	}

	// Обробка масиву reviews
	var reviews []*model.Review
	switch revs := doc["reviews"].(type) {
	case primitive.A:
		for _, r := range revs {
			if reviewMap, ok := r.(map[string]interface{}); ok {
				review, err := convertMapToReview(reviewMap)
				if err != nil {
					return nil, err
				}
				reviews = append(reviews, review)
			}
		}
	case []interface{}:
		for _, r := range revs {
			if reviewMap, ok := r.(map[string]interface{}); ok {
				review, err := convertMapToReview(reviewMap)
				if err != nil {
					return nil, err
				}
				reviews = append(reviews, review)
			}
		}
	}

	return &model.GoogleReview{
		ID:      oid.Hex(),
		Name:    name,
		Rating:  rating,
		Reviews: reviews,
	}, nil
}

// convertMapToReview конвертує карту даних у GraphQL-модель Review.
func convertMapToReview(m map[string]interface{}) (*model.Review, error) {
	authorName, _ := m["author_name"].(string)
	text, _ := m["text"].(string)
	relativeTime, _ := m["relative_time_description"].(string)
	retrievalDate, _ := m["retrieval_date"].(string)
	var rating int
	if r, ok := m["rating"].(int32); ok {
		rating = int(r)
	} else if r, ok := m["rating"].(int); ok {
		rating = r
	} else if r, ok := m["rating"].(float64); ok {
		rating = int(r)
	}
	status, _ := m["status"].(bool)
	idReview, _ := m["id_review"].(string)
	nReviewUser, _ := m["n_review_user"].(string)
	nPhotoUser, _ := m["n_photo_user"].(string)
	urlUser, _ := m["url_user"].(string)

	return &model.Review{
		AuthorName:              authorName,
		Rating:                  rating,
		Text:                    text,
		RelativeTimeDescription: relativeTime,
		RetrievalDate:           retrievalDate,
		Status:                  status,
		IDReview:                idReview,
		NReviewUser:             nReviewUser,
		NPhotoUser:              nPhotoUser,
		URLUser:                 urlUser,
	}, nil
}
