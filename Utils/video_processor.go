package Utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// ProcessVideoToHLS takes an input video and generates a single optimized HLS stream
// Optimized for storage saving and fast loading.
func ProcessVideoToHLS(inputPath, outputDir string) (string, error) {
	log.Printf("START: Optimized HLS Processing for %s -> %s", inputPath, outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	masterPlaylistPath := filepath.Join(outputDir, "master.m3u8")

	// Get original video dimensions
	origWidth, origHeight, err := getVideoDimensions(inputPath)
	if err != nil {
		log.Printf("Error getting dimensions: %v", err)
		return "", err
	}

	isPortrait := origHeight > origWidth

	// Single Standard Resolution: 480p equivalent (720-854px long edge)
	// This provides a good balance between quality and storage size.
	targetLongEdge := 720
	var scaleFilter string
	var targetWidth, targetHeight int

	if isPortrait {
		targetHeight = targetLongEdge
		if targetHeight > origHeight { targetHeight = origHeight }
		scaleFilter = fmt.Sprintf("scale=-2:%d", targetHeight)
		// Estimated width for bandwidth calc
		targetWidth = int(float64(targetHeight) * float64(origWidth) / float64(origHeight))
	} else {
		targetWidth = targetLongEdge
		if targetWidth > origWidth { targetWidth = origWidth }
		scaleFilter = fmt.Sprintf("scale=%d:-2", targetWidth)
		targetHeight = int(float64(targetWidth) * float64(origHeight) / float64(origWidth))
	}

	// Optimized for storage: CRF 28 (higher means more compression), veryfast preset
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-vf", scaleFilter,
		"-c:v", "libx264", "-profile:v", "main", "-crf", "28", "-preset", "veryfast", "-sc_threshold", "0", "-g", "48", "-keyint_min", "48",
		"-c:a", "aac", "-b:a", "96k", "-ac", "2", // Lower audio bitrate to save space
		"-hls_time", "6", "-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "seg_%03d.ts"),
		masterPlaylistPath,
	)

	log.Printf("Transcoding optimized stream (%dx%d)...", targetWidth, targetHeight)
	if err := cmd.Run(); err != nil {
		log.Printf("Error transcoding: %v", err)
		return "", err
	}

	log.Printf("END: Optimized Processing complete")
	return "/uploads/processed/" + filepath.Base(outputDir) + "/master.m3u8", nil
}

func getVideoDimensions(inputPath string) (int, int, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", inputPath)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	var w, h int
	fmt.Sscanf(string(out), "%dx%d", &w, &h)
	return w, h, nil
}
