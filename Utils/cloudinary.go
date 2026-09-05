package Utils

import (
	"context"
	"log"
	"vx-api/Config"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
)

// UploadToCloudinary uploads a file to Cloudinary and returns the secure URL
// resourceType can be "image", "video", or "raw". Audio should use "video".
func UploadToCloudinary(filePath string, resourceType string, folder string) (string, error) {
	if Config.CloudinaryURL == "" || Config.CloudinaryURL == "cloudinary://API_KEY:API_SECRET@CLOUD_NAME" {
		log.Println("Cloudinary URL not configured")
		return "", nil // Return empty to fallback to local
	}

	cld, err := cloudinary.NewFromURL(Config.CloudinaryURL)
	if err != nil {
		log.Printf("Failed to initialize Cloudinary: %v", err)
		return "", err
	}

	if folder == "" {
		folder = "vx-app"
	}

	ctx := context.Background()
	uploadResult, err := cld.Upload.Upload(ctx, filePath, uploader.UploadParams{
		ResourceType: resourceType,
		Folder:       folder,
	})
	if err != nil {
		log.Printf("Failed to upload to Cloudinary: %v", err)
		return "", err
	}

	return uploadResult.SecureURL, nil
}
