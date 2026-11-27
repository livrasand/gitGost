package http

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	bundleLinkTTL = 7 * 24 * time.Hour
	bundlePrefix  = "bundles/"
)

type r2Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func newR2StoreFromEnv() (*r2Store, error) {
	endpoint := os.Getenv("R2_ENDPOINT")
	if endpoint == "" && os.Getenv("R2_ACCOUNT_ID") != "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", os.Getenv("R2_ACCOUNT_ID"))
	}
	key := os.Getenv("R2_ACCESS_KEY_ID")
	secret := os.Getenv("R2_SECRET_ACCESS_KEY")
	bucket := os.Getenv("R2_BUCKET")
	if endpoint == "" || key == "" || secret == "" || bucket == "" {
		return nil, errors.New("bundle storage is not configured: set R2_ACCOUNT_ID (or R2_ENDPOINT), R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY and R2_BUCKET")
	}

	client := s3.New(s3.Options{
		Region:                     "auto",
		Credentials:                credentials.NewStaticCredentialsProvider(key, secret, ""),
		BaseEndpoint:               aws.String(endpoint),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
	})
	return &r2Store{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  bucket,
	}, nil
}

func uploadBundleToR2(ctx context.Context, bundlePath, filename string, size int64, hash string) (*remoteJobResult, error) {
	store, err := newR2StoreFromEnv()
	if err != nil {
		return nil, err
	}

	key := bundlePrefix + hash + ".bundle"
	f, err := os.Open(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()

	_, err = store.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(store.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String("application/octet-stream"),
		Metadata:      map[string]string{"sha256": hash},
	})
	if err != nil {
		return nil, fmt.Errorf("upload bundle to storage: %w", err)
	}

	presigned, err := store.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(bundleLinkTTL))
	if err != nil {
		return nil, fmt.Errorf("presign bundle download: %w", err)
	}

	return &remoteJobResult{
		Slug:        hash,
		Size:        size,
		Sha256:      hash,
		DownloadURL: presigned.URL,
		Filename:    filename,
		ExpiresAt:   time.Now().Add(bundleLinkTTL).UTC().Format(time.RFC3339),
	}, nil
}
