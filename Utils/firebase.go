package Utils

import (
	"context"
	"log"
	"os"
	"path/filepath"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

var fcmClient *messaging.Client

// InitFirebase initializes the Firebase Admin SDK
func InitFirebase() {
	ctx := context.Background()
	serviceAccountPath := filepath.Join("Config", "service-account.json")

	// Check if service account file exists
	if _, err := os.Stat(serviceAccountPath); os.IsNotExist(err) {
		log.Println("Firebase Service Account file NOT found at Config/service-account.json. Skipping Firebase initialization.")
		return
	}

	opt := option.WithServiceAccountFile(serviceAccountPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		log.Printf("error initializing firebase app: %v", err)
		return
	}

	client, err := app.Messaging(ctx)
	if err != nil {
		log.Printf("error getting messaging client: %v", err)
		return
	}

	fcmClient = client
	log.Println("Firebase Admin SDK initialized successfully ✅")
}

// SendFCMNotification sends a push notification to a specific token
func SendFCMNotification(token, title, body string, data map[string]string) {
	if fcmClient == nil || token == "" {
		return
	}

	message := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
		Android: &messaging.AndroidConfig{
			Priority: "high",
			Notification: &messaging.AndroidNotification{
				Sound: "default",
			},
		},
		APNS: &messaging.APNSConfig{
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{
					Sound: "default",
				},
			},
		},
	}

	_, err := fcmClient.Send(context.Background(), message)
	if err != nil {
		log.Printf("error sending fcm message: %v", err)
	}
}
