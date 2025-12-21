package structure_seo

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/Kodeworks/golang-image-ico"
	"github.com/disintegration/imaging"

	"web100now-clients-platform/app/graph/model"
	"web100now-clients-platform/core"
	"web100now-clients-platform/core/db/utils"
	"web100now-clients-platform/core/middleware"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// Resolver is the resolver for retrieving SEO metadata.
type Resolver struct{}

func NewResolver() *Resolver {
	return &Resolver{}
}

// Seo returns SEO data for the specified page (pageKey).
func (r *Resolver) Seo(ctx context.Context, pageKey string) (*model.Seo, error) {
	db, err := utils.GetMongoDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB connect error: %w", err)
	}

	doc, err := fetchOneSEO(ctx, db, pageKey)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("fetch SEO error: %w", err)
	}

	return convertToModel(doc), nil
}

// SeoConfig returns the SEO plugin configuration from the plugins collection.
func (r *Resolver) SeoConfig(ctx context.Context) (*model.SeoConfig, error) {
	db, err := utils.GetMongoDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB connect error: %w", err)
	}

	doc, err := fetchPluginConfig(ctx, db, "structure_seo")
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("SEO plugin configuration not found")
		}
		return nil, fmt.Errorf("fetch SEO config error: %w", err)
	}

	return convertConfigToModel(doc), nil
}

// SeoConfigFull returns the raw plugin configuration (LOCAL_DEVELOPMENT only).
func (r *Resolver) SeoConfigFull(ctx context.Context) (core.JSON, error) {
	if !localDevelopmentEnabled() {
		return nil, errors.New("full SEO config is available only when LOCAL_DEVELOPMENT=true")
	}

	db, err := utils.GetMongoDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB connect error: %w", err)
	}

	doc, err := fetchPluginConfig(ctx, db, "structure_seo")
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("SEO plugin configuration not found")
		}
		return nil, fmt.Errorf("fetch SEO config error: %w", err)
	}

	jsonDoc, err := bsonToJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("convert config error: %w", err)
	}

	return jsonDoc, nil
}

// UpdateSeoConfig updates the plugin configuration (LOCAL_DEVELOPMENT only).
func (r *Resolver) UpdateSeoConfig(ctx context.Context, payload core.JSON) (core.JSON, error) {
	if !localDevelopmentEnabled() {
		return nil, errors.New("configuration updates allowed only when LOCAL_DEVELOPMENT=true")
	}

	db, err := utils.GetMongoDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB connect error: %w", err)
	}

	update := bson.M{}
	if payload != nil {
		update["config"] = map[string]interface{}(payload)
	}

	collection := db.Collection("plugins")
	filter := bson.M{"short_name": "structure_seo"}

	if len(update) > 0 {
		if _, err := collection.UpdateOne(ctx, filter, bson.M{"$set": update}); err != nil {
			return nil, fmt.Errorf("failed to update SEO config: %w", err)
		}
	}

	doc, err := fetchPluginConfig(ctx, db, "structure_seo")
	if err != nil {
		return nil, fmt.Errorf("fetch updated config error: %w", err)
	}

	jsonDoc, err := bsonToJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("convert updated config error: %w", err)
	}

	return jsonDoc, nil
}

// GenerateSeoFavicons processes an uploaded image and generates favicon assets and Next.js-ready bundle.
func (r *Resolver) GenerateSeoFavicons(ctx context.Context, file graphql.Upload) (*model.SeoFaviconPackage, error) {
	if !localDevelopmentEnabled() {
		return nil, errors.New("favicon generation is allowed only when LOCAL_DEVELOPMENT=true")
	}

	if file.File == nil {
		return nil, errors.New("uploaded file is empty")
	}

	db, err := utils.GetMongoDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB connect error: %w", err)
	}

	clientName := resolveClientName(ctx)
	tempPath, err := saveUploadToTemp(&file, "seo-favicon")
	if err != nil {
		return nil, fmt.Errorf("failed to save upload: %w", err)
	}
	defer os.Remove(tempPath)

	srcImage, err := imaging.Open(tempPath, imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("failed to open uploaded image: %w", err)
	}

	timestamp := time.Now().UTC()
	folderName := timestamp.Format("20060102-150405")
	baseDir := filepath.Join("cdn", clientName, "favicons", folderName)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create target directory: %w", err)
	}

	var (
		assets        []*model.SeoFaviconAsset
		assetsForConf []map[string]interface{}
	)

	addAsset := func(name, purpose, mimeType string, sizeLabel *string, localPath string) {
		cdnPath := "/" + filepath.ToSlash(localPath)
		sizeValue := ""
		if sizeLabel != nil {
			sizeValue = *sizeLabel
		}
		assets = append(assets, &model.SeoFaviconAsset{
			Name:    name,
			CdnPath: cdnPath,
			FilePath: func() *string {
				p := localPath
				return &p
			}(),
			Size: func() *string {
				if sizeValue == "" {
					return nil
				}
				p := sizeValue
				return &p
			}(),
			Purpose: func() *string {
				if purpose == "" {
					return nil
				}
				p := purpose
				return &p
			}(),
			Type: mimeType,
		})

		assetMap := map[string]interface{}{
			"name":     name,
			"cdnPath":  cdnPath,
			"purpose":  purpose,
			"type":     mimeType,
			"filePath": localPath,
		}
		if sizeValue != "" {
			assetMap["size"] = sizeValue
		}
		assetsForConf = append(assetsForConf, assetMap)
	}

	// Generate PNG assets
	pngTargets := []struct {
		Size    int
		Name    string
		Purpose string
	}{
		{16, "favicon-16x16.png", "browser_tab"},
		{32, "favicon-32x32.png", "browser_tab"},
		{48, "favicon-48x48.png", "browser_tab"},
		{64, "favicon-64x64.png", "browser_tab"},
		{128, "favicon-128x128.png", "shortcut"},
		{180, "apple-touch-icon.png", "apple_touch"},
		{192, "android-chrome-192x192.png", "android"},
		{256, "android-chrome-256x256.png", "android"},
		{384, "android-chrome-384x384.png", "android"},
		{512, "android-chrome-512x512.png", "android"},
	}

	for _, target := range pngTargets {
		resized := imaging.Fill(srcImage, target.Size, target.Size, imaging.Center, imaging.Lanczos)
		targetPath := filepath.Join(baseDir, target.Name)
		if err := imaging.Save(resized, targetPath, imaging.PNGCompressionLevel(png.BestCompression)); err != nil {
			return nil, fmt.Errorf("failed to save %s: %w", target.Name, err)
		}
		sizeLabel := fmt.Sprintf("%dx%d", target.Size, target.Size)
		addAsset(target.Name, target.Purpose, "image/png", &sizeLabel, targetPath)
	}

	// Generate favicon.ico with multiple sizes
	icoPath := filepath.Join(baseDir, "favicon.ico")
	if err := createICO(srcImage, icoPath); err != nil {
		return nil, fmt.Errorf("failed to create favicon.ico: %w", err)
	}
	addAsset("favicon.ico", "browser_tab", "image/x-icon", nil, icoPath)

	// Generate manifest.json
	manifestIcons := make([]map[string]interface{}, 0, len(assets))
	for _, asset := range assets {
		if !strings.HasSuffix(asset.Name, ".png") {
			continue
		}
		icon := map[string]interface{}{
			"src":  asset.CdnPath,
			"type": asset.Type,
		}
		if asset.Size != nil && *asset.Size != "" {
			icon["sizes"] = *asset.Size
		} else {
			icon["sizes"] = "any"
		}
		if asset.Purpose != nil && *asset.Purpose != "" {
			icon["purpose"] = *asset.Purpose
		} else {
			icon["purpose"] = "any"
		}
		manifestIcons = append(manifestIcons, icon)
	}

	manifestObject := map[string]interface{}{
		"name":             "Generated Favicons",
		"short_name":       "Favicons",
		"icons":            manifestIcons,
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#ffffff",
		"theme_color":      "#ffffff",
	}

	manifestBytes, err := json.MarshalIndent(manifestObject, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(baseDir, "site.webmanifest")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}
	manifestStr := string(manifestBytes)
	addAsset("site.webmanifest", "manifest", "application/manifest+json", nil, manifestPath)

	// Generate browserconfig.xml
	var tilePath string
	for _, asset := range assets {
		if asset.Name == "mstile-150x150.png" {
			tilePath = asset.CdnPath
			break
		}
	}
	if tilePath == "" {
		// create 150x150 tile
		tileImg := imaging.Fill(srcImage, 150, 150, imaging.Center, imaging.Lanczos)
		tileLocal := filepath.Join(baseDir, "mstile-150x150.png")
		if err := imaging.Save(tileImg, tileLocal, imaging.PNGCompressionLevel(png.BestCompression)); err != nil {
			return nil, fmt.Errorf("failed to create mstile image: %w", err)
		}
		sizeLabel := "150x150"
		addAsset("mstile-150x150.png", "windows_tile", "image/png", &sizeLabel, tileLocal)
		tilePath = assets[len(assets)-1].CdnPath
	}

	browserConfig := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<browserconfig>
  <msapplication>
    <tile>
      <square150x150logo src="%s"/>
      <TileColor>#ffffff</TileColor>
    </tile>
  </msapplication>
</browserconfig>
`, tilePath)
	browserConfigPath := filepath.Join(baseDir, "browserconfig.xml")
	if err := os.WriteFile(browserConfigPath, []byte(browserConfig), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write browserconfig.xml: %w", err)
	}
	addAsset("browserconfig.xml", "windows_tile", "application/xml", nil, browserConfigPath)

	// Create instructions
	instructions := []string{
		"Download the generated ZIP archive and extract it into your Next.js app's public/ folder.",
		"Ensure the generated site.webmanifest is referenced from app/layout.tsx or pages/_document.tsx.",
		"Add <link rel=\"icon\" href=\"/favicon.ico\" sizes=\"any\"> and the provided Apple/Android links to your <Head> component.",
		"Restart Next.js dev server if running so it picks up new static assets.",
	}

	// Create ZIP archive
	zipFilePath := filepath.Join(baseDir, "favicons-nextjs.zip")
	if err := createZipArchive(zipFilePath, baseDir); err != nil {
		return nil, fmt.Errorf("failed to create ZIP archive: %w", err)
	}
	zipCdnPath := "/" + filepath.ToSlash(zipFilePath)
	addAsset("favicons-nextjs.zip", "bundle", "application/zip", nil, zipFilePath)

	// Persist metadata to plugins config.public
	if err := updatePluginFavicons(ctx, db, assetsForConf, manifestStr, manifestPath, browserConfig, browserConfigPath, zipFilePath, zipCdnPath, file.Filename, instructions, timestamp); err != nil {
		return nil, fmt.Errorf("failed to persist favicon metadata: %w", err)
	}

	message := "Favicon package generated successfully"
	return &model.SeoFaviconPackage{
		Success: true,
		Message: message,
		Assets:  assets,
		Manifest: func() *string {
			m := manifestStr
			return &m
		}(),
		BrowserConfig: func() *string {
			b := browserConfig
			return &b
		}(),
		Instructions: instructions,
		ZipPath: func() *string {
			p := zipFilePath
			return &p
		}(),
		ZipCdnPath: func() *string {
			p := zipCdnPath
			return &p
		}(),
	}, nil
}

// fetchOneSEO reads one document by pageKey.
func fetchOneSEO(ctx context.Context, db *mongo.Database, pageKey string) (bson.M, error) {
	coll := db.Collection("structure_seo")
	filter := bson.M{"pageKey": pageKey}
	var doc bson.M
	err := coll.FindOne(ctx, filter).Decode(&doc)
	return doc, err
}

// fetchPluginConfig reads plugin configuration from the plugins collection by short_name.
func fetchPluginConfig(ctx context.Context, db *mongo.Database, shortName string) (bson.M, error) {
	coll := db.Collection("plugins")
	filter := bson.M{"short_name": shortName}
	var doc bson.M
	err := coll.FindOne(ctx, filter).Decode(&doc)
	return doc, err
}

// convertConfigToModel transforms plugin config bson.M → *model.SeoConfig.
func convertConfigToModel(doc bson.M) *model.SeoConfig {
	oid, _ := doc["_id"].(primitive.ObjectID)

	name := getString(doc, "name")
	shortName := getString(doc, "short_name")
	version := getString(doc, "version")
	description := getString(doc, "description")
	author := getString(doc, "author")
	active := getBool(doc, "active")

	// Get config object
	config := getMap(doc, "config")
	if config == nil {
		config = make(map[string]interface{})
	}

	// Get dates (can be DateTime, string, or time.Time)
	var createdAt, updatedAt *string
	if createdAtVal := doc["createdAt"]; createdAtVal != nil {
		if dt, ok := createdAtVal.(primitive.DateTime); ok {
			createdAtStr := dt.Time().Format("2006-01-02T15:04:05.000Z")
			createdAt = &createdAtStr
		} else if str, ok := createdAtVal.(string); ok {
			createdAt = &str
		}
	}
	if updatedAtVal := doc["updatedAt"]; updatedAtVal != nil {
		if dt, ok := updatedAtVal.(primitive.DateTime); ok {
			updatedAtStr := dt.Time().Format("2006-01-02T15:04:05.000Z")
			updatedAt = &updatedAtStr
		} else if str, ok := updatedAtVal.(string); ok {
			updatedAt = &str
		}
	}

	// Get localBusiness from config
	var localBusiness *model.LocalBusinessSchema
	if lb := getMap(config, "localBusiness"); lb != nil {
		localBusiness = convertLocalBusinessSchema(lb)
	}

	return &model.SeoConfig{
		ID:                    oid.Hex(),
		Name:                  name,
		ShortName:             shortName,
		Version:               version,
		Description:           description,
		Author:                author,
		Active:                active,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		FaviconURL:            optional(getString(config, "faviconUrl")),
		DefaultLocale:         optional(getString(config, "defaultLocale")),
		CookieConsentRequired: getBoolPtr(config, "cookieConsentRequired"),
		RobotsURL:             optional(getString(config, "robotsUrl")),
		Webmanifest:           optional(getString(config, "webmanifest")),
		LocalBusiness:         localBusiness,
	}
}

// convertToModel transforms bson.M → *model.Seo with support for all fields.
func convertToModel(doc bson.M) *model.Seo {
	oid, _ := doc["_id"].(primitive.ObjectID)
	pageKey := getString(doc, "pageKey")

	// Basic SEO fields
	title := getString(doc, "title")
	desc := getString(doc, "metaDescription")
	keywords := getStrings(doc, "keywords")
	author := getString(doc, "author")
	canonical := getString(doc, "canonical")
	viewport := getString(doc, "viewport")
	robots := getString(doc, "robots")

	// Additional HTML meta tags
	expires := optional(getString(doc, "expires"))
	rating := optional(getString(doc, "rating"))
	contentLanguage := optional(getString(doc, "contentLanguage"))
	themeColor := optional(getString(doc, "themeColor"))
	referrer := optional(getString(doc, "referrer"))
	generator := optional(getString(doc, "generator"))
	copyright := optional(getString(doc, "copyright"))
	revisitAfter := optional(getString(doc, "revisitAfter"))
	distribution := optional(getString(doc, "distribution"))
	formatDetection := optional(getString(doc, "formatDetection"))
	geoRegion := optional(getString(doc, "geoRegion"))
	geoPosition := optional(getString(doc, "geoPosition"))

	// Nested objects
	og := getMap(doc, "openGraph")
	tc := getMap(doc, "twitterCard")
	fb := getMap(doc, "facebook")
	li := getMap(doc, "linkedIn")
	dc := getMap(doc, "dublinCore")
	sd := getMap(doc, "structuredData")
	al := getArray(doc, "alternateLanguages")

	return &model.Seo{
		ID:              oid.Hex(),
		Page:            defaultStr(getString(doc, "page"), defaultStr(pageKey, "/")),
		PageKey:         pageKey,
		Title:           title,
		MetaDescription: desc,
		Keywords:        keywords,
		Author:          author,
		Canonical:       canonical,
		Viewport:        viewport,
		Robots:          robots,

		// Additional fields
		Expires:         expires,
		Rating:          rating,
		ContentLanguage: contentLanguage,
		ThemeColor:      themeColor,
		Referrer:        referrer,
		Generator:       generator,
		Copyright:       copyright,
		RevisitAfter:    revisitAfter,
		Distribution:    distribution,
		FormatDetection: formatDetection,
		GeoRegion:       geoRegion,
		GeoPosition:     geoPosition,

		// OpenGraph
		OpenGraph: convertOpenGraph(og),

		// Twitter Card
		TwitterCard: convertTwitterCard(tc),

		// Facebook
		Facebook: convertFacebook(fb),

		// LinkedIn
		LinkedIn: convertLinkedIn(li),

		// Dublin Core
		DublinCore: convertDublinCore(dc),

		// Structured Data
		StructuredData: convertStructuredData(sd),

		// Alternate Languages
		AlternateLanguages: convertAlternateLanguages(al),

		// Additional social
		PinterestVerification:    optional(getString(doc, "pinterestVerification")),
		AppleMobileWebAppCapable: optional(getString(doc, "appleMobileWebAppCapable")),
		MsTileColor:              optional(getString(doc, "msTileColor")),

		// Additional SEO
		LastModified:    optional(getString(doc, "lastModified")),
		Priority:        getFloat(doc, "priority"),
		ChangeFrequency: optional(getString(doc, "changeFrequency")),
	}
}

// convertOpenGraph converts Open Graph metadata
func convertOpenGraph(og map[string]interface{}) *model.OpenGraphMeta {
	if og == nil {
		return &model.OpenGraphMeta{
			OgTitle:       "",
			OgDescription: "",
			OgImage:       "",
			OgURL:         "",
			OgType:        "website",
		}
	}

	return &model.OpenGraphMeta{
		OgTitle:          getString(og, "og:title"),
		OgDescription:    getString(og, "og:description"),
		OgImage:          getString(og, "og:image"),
		OgURL:            getString(og, "og:url"),
		OgType:           getString(og, "og:type"),
		OgLocale:         optional(getString(og, "og:locale")),
		OgSiteName:       optional(getString(og, "og:site_name")),
		OgImageWidth:     getInt(og, "og:image:width"),
		OgImageHeight:    getInt(og, "og:image:height"),
		OgImageAlt:       optional(getString(og, "og:image:alt")),
		OgImageType:      optional(getString(og, "og:image:type")),
		OgImageSecureURL: optional(getString(og, "og:image:secure_url")),
		OgVideo:          optional(getString(og, "og:video")),
		OgVideoWidth:     getInt(og, "og:video:width"),
		OgVideoHeight:    getInt(og, "og:video:height"),
		OgVideoType:      optional(getString(og, "og:video:type")),
		OgVideoSecureURL: optional(getString(og, "og:video:secure_url")),
		OgAudio:          optional(getString(og, "og:audio")),
		OgAudioType:      optional(getString(og, "og:audio:type")),
		OgAudioSecureURL: optional(getString(og, "og:audio:secure_url")),
		OgUpdatedTime:    optional(getString(og, "og:updated_time")),
		OgPublishedTime:  optional(getString(og, "article:published_time")),
		OgExpirationTime: optional(getString(og, "og:expiration_time")),
		OgAuthor:         optional(getString(og, "article:author")),
		OgSection:        optional(getString(og, "article:section")),
		OgTag:            getStrings(og, "article:tag"),
		OgPriceAmount:    getFloat(og, "product:price:amount"),
		OgPriceCurrency:  optional(getString(og, "product:price:currency")),
		OgDeterminer:     optional(getString(og, "og:determiner")),
		OgImageURL:       optional(getString(og, "og:image:url")),
	}
}

// convertTwitterCard converts Twitter Card metadata
func convertTwitterCard(tc map[string]interface{}) *model.TwitterCardMeta {
	if tc == nil {
		return &model.TwitterCardMeta{
			TwitterCard:        "summary",
			TwitterTitle:       "",
			TwitterDescription: "",
			TwitterImage:       "",
		}
	}

	return &model.TwitterCardMeta{
		TwitterCard:              getString(tc, "twitter:card"),
		TwitterTitle:             getString(tc, "twitter:title"),
		TwitterDescription:       getString(tc, "twitter:description"),
		TwitterImage:             getString(tc, "twitter:image"),
		TwitterSite:              optional(getString(tc, "twitter:site")),
		TwitterCreator:           optional(getString(tc, "twitter:creator")),
		TwitterImageAlt:          optional(getString(tc, "twitter:image:alt")),
		TwitterPlayer:            optional(getString(tc, "twitter:player")),
		TwitterPlayerWidth:       getInt(tc, "twitter:player:width"),
		TwitterPlayerHeight:      getInt(tc, "twitter:player:height"),
		TwitterPlayerStream:      optional(getString(tc, "twitter:player:stream")),
		TwitterAppNameiPhone:     optional(getString(tc, "twitter:app:name:iphone")),
		TwitterAppIdiPhone:       optional(getString(tc, "twitter:app:id:iphone")),
		TwitterAppURLiPhone:      optional(getString(tc, "twitter:app:url:iphone")),
		TwitterAppNameiPad:       optional(getString(tc, "twitter:app:name:ipad")),
		TwitterAppIdiPad:         optional(getString(tc, "twitter:app:id:ipad")),
		TwitterAppURLiPad:        optional(getString(tc, "twitter:app:url:ipad")),
		TwitterAppNameGooglePlay: optional(getString(tc, "twitter:app:name:googleplay")),
		TwitterAppIDGooglePlay:   optional(getString(tc, "twitter:app:id:googleplay")),
		TwitterAppURLGooglePlay:  optional(getString(tc, "twitter:app:url:googleplay")),
	}
}

// convertFacebook converts Facebook metadata
func convertFacebook(fb map[string]interface{}) *model.FacebookMeta {
	if fb == nil {
		return nil
	}

	return &model.FacebookMeta{
		FbAppID:              optional(getString(fb, "fb:app_id")),
		FbAdmins:             optional(getString(fb, "fb:admins")),
		FbPages:              optional(getString(fb, "fb:pages")),
		ArticleAuthor:        optional(getString(fb, "article:author")),
		ArticlePublisher:     optional(getString(fb, "article:publisher")),
		ArticlePublishedTime: optional(getString(fb, "article:published_time")),
		ArticleModifiedTime:  optional(getString(fb, "article:modified_time")),
		ArticleSection:       optional(getString(fb, "article:section")),
		ArticleTag:           getStrings(fb, "article:tag"),
	}
}

// convertLinkedIn converts LinkedIn metadata
func convertLinkedIn(li map[string]interface{}) *model.LinkedInMeta {
	if li == nil {
		return nil
	}

	return &model.LinkedInMeta{
		LinkedInOwner: optional(getString(li, "linkedin:owner")),
		LinkedInImage: optional(getString(li, "linkedin:image")),
	}
}

// convertDublinCore converts Dublin Core metadata
func convertDublinCore(dc map[string]interface{}) *model.DublinCoreMeta {
	if dc == nil {
		return &model.DublinCoreMeta{
			DCTitle:   "",
			DCCreator: "",
		}
	}

	return &model.DublinCoreMeta{
		DCTitle:       getString(dc, "DC:title"),
		DCCreator:     getString(dc, "DC:creator"),
		DCSubject:     optional(getString(dc, "DC:subject")),
		DCDescription: optional(getString(dc, "DC:description")),
		DCPublisher:   optional(getString(dc, "DC:publisher")),
		DCContributor: optional(getString(dc, "DC:contributor")),
		DCDate:        optional(getString(dc, "DC:date")),
		DCType:        optional(getString(dc, "DC:type")),
		DCFormat:      optional(getString(dc, "DC:format")),
		DCIdentifier:  optional(getString(dc, "DC:identifier")),
		DCSource:      optional(getString(dc, "DC:source")),
		DCLanguage:    optional(getString(dc, "DC:language")),
		DCRelation:    optional(getString(dc, "DC:relation")),
		DCCoverage:    optional(getString(dc, "DC:coverage")),
		DCRights:      optional(getString(dc, "DC:rights")),
	}
}

// convertStructuredData converts structured data
func convertStructuredData(sd map[string]interface{}) *model.StructuredData {
	if sd == nil {
		return nil
	}

	return &model.StructuredData{
		JSONLd:         optional(getString(sd, "jsonLd")),
		Organization:   convertOrganizationSchema(getMap(sd, "organization")),
		Website:        convertWebsiteSchema(getMap(sd, "website")),
		BreadcrumbList: convertBreadcrumbList(getMap(sd, "breadcrumbList")),
		Article:        convertArticleSchema(getMap(sd, "article")),
		Product:        convertProductSchema(getMap(sd, "product")),
		LocalBusiness:  convertLocalBusinessSchema(getMap(sd, "localBusiness")),
		Person:         convertPersonSchema(getMap(sd, "person")),
		FaqPage:        convertFAQPageSchema(getMap(sd, "faqPage")),
		VideoObject:    convertVideoObjectSchema(getMap(sd, "videoObject")),
		Review:         convertReviewSchema(getMap(sd, "review")),
	}
}

// convertAlternateLanguages converts alternate languages
func convertAlternateLanguages(al []interface{}) []*model.AlternateLanguage {
	if al == nil {
		return nil
	}

	result := make([]*model.AlternateLanguage, 0, len(al))
	for _, item := range al {
		if m, ok := item.(map[string]interface{}); ok {
			result = append(result, &model.AlternateLanguage{
				Hreflang: getString(m, "hreflang"),
				Href:     getString(m, "href"),
			})
		}
	}
	return result
}

// Schema.org type conversions
func convertOrganizationSchema(org map[string]interface{}) *model.OrganizationSchema {
	if org == nil {
		return nil
	}

	return &model.OrganizationSchema{
		Name:         getString(org, "name"),
		URL:          optional(getString(org, "url")),
		Logo:         optional(getString(org, "logo")),
		Description:  optional(getString(org, "description")),
		ContactPoint: convertContactPoint(getMap(org, "contactPoint")),
		SameAs:       getStrings(org, "sameAs"),
		Address:      convertPostalAddress(getMap(org, "address")),
		Telephone:    optional(getString(org, "telephone")),
		Email:        optional(getString(org, "email")),
	}
}

func convertWebsiteSchema(web map[string]interface{}) *model.WebsiteSchema {
	if web == nil {
		return nil
	}

	sa := getMap(web, "potentialAction")
	var searchAction *model.SearchActionSchema
	if sa != nil {
		searchAction = &model.SearchActionSchema{
			Target:     getString(sa, "target"),
			QueryInput: getString(sa, "query-input"),
		}
	}

	return &model.WebsiteSchema{
		Name:            getString(web, "name"),
		URL:             getString(web, "url"),
		Description:     optional(getString(web, "description")),
		PotentialAction: searchAction,
	}
}

func convertBreadcrumbList(bc map[string]interface{}) *model.BreadcrumbListSchema {
	if bc == nil {
		return nil
	}

	items := getArray(bc, "itemListElement")
	breadcrumbs := make([]*model.ListItemSchema, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]interface{}); ok {
			pos := getInt(m, "position")
			posVal := 0
			if pos != nil {
				posVal = *pos
			}
			breadcrumbs = append(breadcrumbs, &model.ListItemSchema{
				Position: posVal,
				Name:     getString(m, "name"),
				Item:     getString(m, "item"),
			})
		}
	}

	return &model.BreadcrumbListSchema{
		ItemListElement: breadcrumbs,
	}
}

func convertArticleSchema(art map[string]interface{}) *model.ArticleSchema {
	if art == nil {
		return nil
	}

	return &model.ArticleSchema{
		Headline:       getString(art, "headline"),
		Description:    optional(getString(art, "description")),
		Image:          getStrings(art, "image"),
		DatePublished:  optional(getString(art, "datePublished")),
		DateModified:   optional(getString(art, "dateModified")),
		Author:         convertPersonSchema(getMap(art, "author")),
		Publisher:      convertOrganizationSchema(getMap(art, "publisher")),
		ArticleSection: optional(getString(art, "articleSection")),
		ArticleBody:    optional(getString(art, "articleBody")),
		WordCount:      getInt(art, "wordCount"),
	}
}

func convertProductSchema(prod map[string]interface{}) *model.ProductSchema {
	if prod == nil {
		return nil
	}

	brand := getMap(prod, "brand")
	var brandSchema *model.BrandSchema
	if brand != nil {
		brandSchema = &model.BrandSchema{
			Name: getString(brand, "name"),
			Logo: optional(getString(brand, "logo")),
		}
	}

	offers := getArray(prod, "offers")
	var offerSchema *model.OfferSchema
	if len(offers) > 0 {
		if offer, ok := offers[0].(map[string]interface{}); ok {
			seller := convertOrganizationSchema(getMap(offer, "seller"))
			offerSchema = &model.OfferSchema{
				Price:           getString(offer, "price"),
				PriceCurrency:   getString(offer, "priceCurrency"),
				Availability:    optional(getString(offer, "availability")),
				URL:             optional(getString(offer, "url")),
				PriceValidUntil: optional(getString(offer, "priceValidUntil")),
				ItemCondition:   optional(getString(offer, "itemCondition")),
				Seller:          seller,
			}
		}
	}

	return &model.ProductSchema{
		Name:            getString(prod, "name"),
		Description:     optional(getString(prod, "description")),
		Image:           getStrings(prod, "image"),
		Brand:           brandSchema,
		Offers:          offerSchema,
		Sku:             optional(getString(prod, "sku")),
		Gtin:            optional(getString(prod, "gtin")),
		Mpn:             optional(getString(prod, "mpn")),
		AggregateRating: convertAggregateRating(getMap(prod, "aggregateRating")),
		Review:          convertReviewsArray(getArray(prod, "review")),
	}
}

func convertLocalBusinessSchema(lb map[string]interface{}) *model.LocalBusinessSchema {
	if lb == nil {
		return nil
	}

	geo := getMap(lb, "geo")
	var geoCoords *model.GeoCoordinatesSchema
	if geo != nil {
		lat := getFloat(geo, "latitude")
		lon := getFloat(geo, "longitude")
		latVal := 0.0
		lonVal := 0.0
		if lat != nil {
			latVal = *lat
		}
		if lon != nil {
			lonVal = *lon
		}
		geoCoords = &model.GeoCoordinatesSchema{
			Latitude:  latVal,
			Longitude: lonVal,
		}
	}

	hours := getArray(lb, "openingHoursSpecification")
	hoursSpecs := make([]*model.OpeningHoursSpecificationSchema, 0, len(hours))
	for _, h := range hours {
		if m, ok := h.(map[string]interface{}); ok {
			hoursSpecs = append(hoursSpecs, &model.OpeningHoursSpecificationSchema{
				DayOfWeek: getStrings(m, "dayOfWeek"),
				Opens:     optional(getString(m, "opens")),
				Closes:    optional(getString(m, "closes")),
			})
		}
	}

	return &model.LocalBusinessSchema{
		Name:                      getString(lb, "name"),
		Image:                     getStrings(lb, "image"),
		Address:                   convertPostalAddress(getMap(lb, "address")),
		Geo:                       geoCoords,
		Telephone:                 optional(getString(lb, "telephone")),
		PriceRange:                optional(getString(lb, "priceRange")),
		OpeningHoursSpecification: hoursSpecs,
		AggregateRating:           convertAggregateRating(getMap(lb, "aggregateRating")),
	}
}

func convertPersonSchema(person map[string]interface{}) *model.PersonSchema {
	if person == nil {
		return nil
	}

	return &model.PersonSchema{
		Name:      getString(person, "name"),
		URL:       optional(getString(person, "url")),
		Image:     optional(getString(person, "image")),
		JobTitle:  optional(getString(person, "jobTitle")),
		WorksFor:  convertOrganizationSchema(getMap(person, "worksFor")),
		SameAs:    getStrings(person, "sameAs"),
		Email:     optional(getString(person, "email")),
		Telephone: optional(getString(person, "telephone")),
	}
}

func convertFAQPageSchema(faq map[string]interface{}) *model.FAQPageSchema {
	if faq == nil {
		return nil
	}

	questions := getArray(faq, "mainEntity")
	qaList := make([]*model.QuestionSchema, 0, len(questions))
	for _, q := range questions {
		if qMap, ok := q.(map[string]interface{}); ok {
			answer := getMap(qMap, "acceptedAnswer")
			var answerSchema *model.AnswerSchema
			if answer != nil {
				answerSchema = &model.AnswerSchema{
					Text: getString(answer, "text"),
				}
			}
			qaList = append(qaList, &model.QuestionSchema{
				Name:           getString(qMap, "name"),
				AcceptedAnswer: answerSchema,
			})
		}
	}

	return &model.FAQPageSchema{
		MainEntity: qaList,
	}
}

func convertVideoObjectSchema(video map[string]interface{}) *model.VideoObjectSchema {
	if video == nil {
		return nil
	}

	return &model.VideoObjectSchema{
		Name:         getString(video, "name"),
		Description:  optional(getString(video, "description")),
		ThumbnailURL: getStrings(video, "thumbnailUrl"),
		UploadDate:   optional(getString(video, "uploadDate")),
		Duration:     optional(getString(video, "duration")),
		ContentURL:   optional(getString(video, "contentUrl")),
		EmbedURL:     optional(getString(video, "embedUrl")),
	}
}

func convertReviewSchema(review map[string]interface{}) *model.ReviewSchema {
	if review == nil {
		return nil
	}

	rating := getMap(review, "reviewRating")
	var ratingSchema *model.RatingSchema
	if rating != nil {
		rv := getFloat(rating, "ratingValue")
		rvVal := 0.0
		if rv != nil {
			rvVal = *rv
		}
		ratingSchema = &model.RatingSchema{
			RatingValue: rvVal,
			BestRating:  getFloat(rating, "bestRating"),
			WorstRating: getFloat(rating, "worstRating"),
		}
	}

	return &model.ReviewSchema{
		Author:        convertPersonSchema(getMap(review, "author")),
		DatePublished: optional(getString(review, "datePublished")),
		ReviewBody:    optional(getString(review, "reviewBody")),
		ReviewRating:  ratingSchema,
		ItemReviewed:  optional(getString(review, "itemReviewed")),
	}
}

// Helper functions for conversion
func convertContactPoint(cp map[string]interface{}) *model.ContactPointSchema {
	if cp == nil {
		return nil
	}

	return &model.ContactPointSchema{
		Telephone:         optional(getString(cp, "telephone")),
		ContactType:       optional(getString(cp, "contactType")),
		Email:             optional(getString(cp, "email")),
		AreaServed:        optional(getString(cp, "areaServed")),
		AvailableLanguage: getStrings(cp, "availableLanguage"),
	}
}

func convertPostalAddress(addr map[string]interface{}) *model.PostalAddressSchema {
	if addr == nil {
		return nil
	}

	return &model.PostalAddressSchema{
		StreetAddress:   optional(getString(addr, "streetAddress")),
		AddressLocality: getString(addr, "addressLocality"),
		AddressRegion:   optional(getString(addr, "addressRegion")),
		PostalCode:      optional(getString(addr, "postalCode")),
		AddressCountry:  getString(addr, "addressCountry"),
	}
}

func convertAggregateRating(ar map[string]interface{}) *model.AggregateRatingSchema {
	if ar == nil {
		return nil
	}

	rv := getFloat(ar, "ratingValue")
	rvVal := 0.0
	if rv != nil {
		rvVal = *rv
	}

	rc := getInt(ar, "reviewCount")
	rcVal := 0
	if rc != nil {
		rcVal = *rc
	}

	return &model.AggregateRatingSchema{
		RatingValue: rvVal,
		ReviewCount: rcVal,
		BestRating:  getFloat(ar, "bestRating"),
		WorstRating: getFloat(ar, "worstRating"),
	}
}

func convertReviewsArray(reviews []interface{}) []*model.ReviewSchema {
	if reviews == nil {
		return nil
	}

	result := make([]*model.ReviewSchema, 0, len(reviews))
	for _, r := range reviews {
		if m, ok := r.(map[string]interface{}); ok {
			result = append(result, convertReviewSchema(m))
		}
	}
	return result
}

// --- BASIC HELPER FUNCTIONS ---

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	if v, ok := m[key].(primitive.M); ok {
		return map[string]interface{}(v)
	}
	return nil
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getStrings(m map[string]interface{}, key string) []string {
	out := []string{}
	arr, ok := m[key].([]interface{})
	if !ok {
		return out
	}
	for _, i := range arr {
		if s, ok := i.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func getArray(m map[string]interface{}, key string) []interface{} {
	if arr, ok := m[key].([]interface{}); ok {
		return arr
	}
	if arr, ok := m[key].(primitive.A); ok {
		result := make([]interface{}, len(arr))
		for i, v := range arr {
			result[i] = v
		}
		return result
	}
	return nil
}

func getInt(m map[string]interface{}, key string) *int {
	if v, ok := m[key].(int); ok {
		return &v
	}
	if v, ok := m[key].(int32); ok {
		val := int(v)
		return &val
	}
	if v, ok := m[key].(int64); ok {
		val := int(v)
		return &val
	}
	if v, ok := m[key].(float64); ok {
		val := int(v)
		return &val
	}
	return nil
}

func getFloat(m map[string]interface{}, key string) *float64 {
	if v, ok := m[key].(float64); ok {
		return &v
	}
	if v, ok := m[key].(float32); ok {
		val := float64(v)
		return &val
	}
	if v, ok := m[key].(int); ok {
		val := float64(v)
		return &val
	}
	return nil
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getBoolPtr(m map[string]interface{}, key string) *bool {
	if v, ok := m[key].(bool); ok {
		return &v
	}
	return nil
}

const defaultClientDir = "default"

func localDevelopmentEnabled() bool {
	return strings.ToLower(os.Getenv("LOCAL_DEVELOPMENT")) == "true"
}

func resolveClientName(ctx context.Context) string {
	clientDataAny := ctx.Value(middleware.ClientDataKey)
	if clientDataAny == nil {
		return defaultClientDir
	}
	if cd, ok := clientDataAny.(*middleware.ClientData); ok {
		return sanitizeClientDirectoryName(cd.ClientName)
	}
	return defaultClientDir
}

func sanitizeClientDirectoryName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return defaultClientDir
	}
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		case r == ' ':
			builder.WriteRune('-')
		}
	}
	if builder.Len() == 0 {
		return defaultClientDir
	}
	return builder.String()
}

func saveUploadToTemp(upload *graphql.Upload, prefix string) (string, error) {
	if upload == nil || upload.File == nil {
		return "", errors.New("empty upload")
	}

	ext := filepath.Ext(upload.Filename)
	if ext == "" && upload.ContentType != "" {
		if exts, _ := mime.ExtensionsByType(upload.ContentType); len(exts) > 0 {
			ext = exts[0]
		}
	}
	if ext == "" {
		ext = ".bin"
	}

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("%s-*%s", prefix, ext))
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if seeker, ok := upload.File.(io.Seeker); ok {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
	}

	if _, err := io.Copy(tmpFile, upload.File); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

func createICO(src image.Image, targetPath string) error {
	resized := imaging.Resize(src, 64, 64, imaging.Lanczos)
	nrgba := image.NewNRGBA(resized.Bounds())
	draw.Draw(nrgba, nrgba.Bounds(), resized, image.Point{}, draw.Src)
	file, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return ico.Encode(file, nrgba)
}

func createZipArchive(zipPath, baseDir string) error {
	if err := os.RemoveAll(zipPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	file, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	return filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if path == zipPath {
			return nil
		}

		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}

		zipFile, err := writer.Create(filepath.ToSlash(relPath))
		if err != nil {
			return err
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(zipFile, srcFile)
		closeErr := srcFile.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	})
}

func updatePluginFavicons(
	ctx context.Context,
	db *mongo.Database,
	assets []map[string]interface{},
	manifest string,
	manifestPath string,
	browserConfig string,
	browserConfigPath string,
	zipPath string,
	zipCdnPath string,
	sourceFile string,
	instructions []string,
	generatedAt time.Time,
) error {
	doc, err := fetchPluginConfig(ctx, db, "structure_seo")
	if err != nil {
		return err
	}

	config := cloneGenericMap(getMap(doc, "config"))
	if config == nil {
		config = make(map[string]interface{})
	}

	publicSlice := toInterfaceSlice(config["public"])
	filtered := make([]interface{}, 0, len(publicSlice))
	for _, item := range publicSlice {
		if m := toGenericMap(item); m != nil {
			if key, _ := m["key"].(string); key == "favicons" {
				continue
			}
		}
		filtered = append(filtered, item)
	}

	manifestCdn := "/" + filepath.ToSlash(manifestPath)
	browserConfigCdn := "/" + filepath.ToSlash(browserConfigPath)

	faviconsEntry := map[string]interface{}{
		"key":                  "favicons",
		"generatedAt":          generatedAt.Format(time.RFC3339),
		"sourceFile":           sourceFile,
		"assets":               assets,
		"manifest":             manifest,
		"manifestPath":         manifestCdn,
		"browserConfig":        browserConfig,
		"browserConfigPath":    browserConfigCdn,
		"zip":                  map[string]interface{}{"path": zipPath, "cdnPath": zipCdnPath},
		"instructions":         instructions,
		"realFaviconGenerator": "https://realfavicongenerator.net/",
	}

	config["public"] = append([]interface{}{faviconsEntry}, filtered...)
	config["faviconUrl"] = findAssetCDN(assets, "favicon-32x32.png")
	config["appleTouchIcon"] = findAssetCDN(assets, "apple-touch-icon.png")
	config["androidIcon"] = findAssetCDN(assets, "android-chrome-192x192.png")
	config["manifestUrl"] = manifestCdn
	config["browserConfigUrl"] = browserConfigCdn
	config["faviconsZip"] = map[string]interface{}{"path": zipPath, "cdnPath": zipCdnPath}
	config["lastFaviconGeneratedAt"] = generatedAt.Format(time.RFC3339)

	collection := db.Collection("plugins")
	_, err = collection.UpdateOne(ctx, bson.M{"short_name": "structure_seo"}, bson.M{
		"$set": bson.M{"config": config},
	})
	return err
}

func findAssetCDN(assets []map[string]interface{}, name string) string {
	for _, asset := range assets {
		if assetName, _ := asset["name"].(string); assetName == name {
			if cdn, _ := asset["cdnPath"].(string); cdn != "" {
				return cdn
			}
		}
	}
	return ""
}

func cloneGenericMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = deepCopyValueGeneric(v)
	}
	return dst
}

func deepCopyValueGeneric(value interface{}) interface{} {
	switch val := value.(type) {
	case map[string]interface{}:
		return cloneGenericMap(val)
	case primitive.M:
		return cloneGenericMap(map[string]interface{}(val))
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = deepCopyValueGeneric(item)
		}
		return out
	case primitive.A:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = deepCopyValueGeneric(item)
		}
		return out
	default:
		return val
	}
}

func toInterfaceSlice(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case primitive.A:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	default:
		return nil
	}
}

func toGenericMap(value interface{}) map[string]interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return v
	case primitive.M:
		return map[string]interface{}(v)
	default:
		return nil
	}
}

func bsonToJSON(doc bson.M) (core.JSON, error) {
	raw, err := bson.MarshalExtJSON(doc, false, true)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}
