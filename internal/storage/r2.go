package storage

import (
	"context"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Storage struct {
	client       *s3.Client
	bucketName   string
	publicDomain string
	enabled      bool
}

var globalR2Storage *R2Storage

func InitR2Storage() {
	accountID := strings.TrimSpace(os.Getenv("R2_ACCOUNT_ID"))
	accessKey := strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY"))
	bucketName := strings.TrimSpace(os.Getenv("R2_BUCKET_NAME"))
	publicDomain := strings.TrimSpace(os.Getenv("R2_PUBLIC_DOMAIN"))

	if accountID == "" || accessKey == "" || secretKey == "" || bucketName == "" {
		globalR2Storage = &R2Storage{enabled: false}
		fmt.Println("[Storage] Cloudflare R2 credentials missing. Using local disk storage fallback.")
		return
	}

	r2Endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL:               r2Endpoint,
			HostnameImmutable: true,
			SigningRegion:     "auto",
		}, nil
	})

	cfg := aws.Config{
		Region:                      "auto",
		Credentials:                 credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		EndpointResolverWithOptions: customResolver,
	}

	client := s3.NewFromConfig(cfg)

	// Clean public domain prefix if present
	publicDomain = strings.TrimSuffix(publicDomain, "/")

	globalR2Storage = &R2Storage{
		client:       client,
		bucketName:   bucketName,
		publicDomain: publicDomain,
		enabled:      true,
	}

	fmt.Printf("[Storage] Cloudflare R2 initialized successfully (Bucket: %s)\n", bucketName)
}

func IsR2Enabled() bool {
	return globalR2Storage != nil && globalR2Storage.enabled
}

func UploadToR2(ctx context.Context, fileHeader *multipart.FileHeader, folder string) (string, error) {
	if !IsR2Enabled() {
		return "", fmt.Errorf("R2 storage not configured")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	filename := fmt.Sprintf("%s/%d_%s", folder, time.Now().UnixNano(), filepath.Base(fileHeader.Filename))

	input := &s3.PutObjectInput{
		Bucket:      aws.String(globalR2Storage.bucketName),
		Key:         aws.String(filename),
		Body:        src,
		ContentType: aws.String(getMimeType(ext)),
	}

	_, err = globalR2Storage.client.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("R2 PutObject error: %w", err)
	}

	if globalR2Storage.publicDomain != "" {
		return fmt.Sprintf("%s/%s", globalR2Storage.publicDomain, filename), nil
	}

	return fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s", globalR2Storage.bucketName, filename), nil
}

func getMimeType(ext string) string {
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".epub":
		return "application/epub+zip"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
