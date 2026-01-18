package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// RequestPayload must match the server's expectation
type RequestPayload struct {
	RepoURL    string `json:"repo_url"`
	TargetOS   string `json:"target_os"`
	TargetArch string `json:"target_arch"`
}

func main() {
	// 1. Parse Command Line Flags
	repo := flag.String("repo", "", "The GitHub repository URL (e.g., github.com/fyne-io/fyne)")
	targetOS := flag.String("os", "windows", "Target Operating System (linux, windows)")
	targetArch := flag.String("arch", "amd64", "Target Architecture (amd64, arm64)")
	serviceURL := flag.String("url", "", "The full URL of your Cloud Run service (e.g., https://go-builder-xyz.run.app/build)")
	flag.Parse()

	if *repo == "" || *serviceURL == "" {
		fmt.Println("Error: --repo and --url are required.")
		flag.Usage()
		os.Exit(1)
	}

	// 2. Prepare the JSON Payload
	payload := RequestPayload{
		RepoURL:    *repo,
		TargetOS:   *targetOS,
		TargetArch: *targetArch,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	// 3. Create the Request
	req, err := http.NewRequest("POST", *serviceURL, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	// "Accept: text/event-stream" tells the server (and proxies) we want streaming
	req.Header.Set("Accept", "text/event-stream")

	// 4. Execute the Request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Network error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Server returned error: %s\n", resp.Status)
		os.Exit(1)
	}

	fmt.Printf("🚀 Connecting to builder service for %s [%s/%s]...\n\n", *repo, *targetOS, *targetArch)

	// 5. Stream the Response (SSE Parsing)
	scanner := bufio.NewScanner(resp.Body)

	// Default buffer size might be too small for very long lines, but fine for status messages
	for scanner.Scan() {
		line := scanner.Text()

		// SSE format usually sends "data: <message>"
		// It may also send empty lines as keep-alives or separators
		if strings.HasPrefix(line, "data:") {
			message := strings.TrimPrefix(line, "data:")
			message = strings.TrimSpace(message)

			// Check for our custom "close" signal (optional, loop triggers EOF anyway)
			if message == "close" {
				break
			}
			if message != "" {
				// Print with a checkmark for visual clarity
				fmt.Printf("✅ %s\n", message)
			}
		} else if strings.HasPrefix(line, "event:") {
			// Handle specific event types if needed (e.g., "event: error")
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("\n❌ Connection interrupted: %v\n", err)
	} else {
		fmt.Println("\n✨ Build process finished.")
	}
}
