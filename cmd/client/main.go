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
	"time"
)

type RequestPayload struct {
	RepoURL    string `json:"repo_url"`
	TargetOS   string `json:"target_os"`
	TargetArch string `json:"target_arch"`
}

func main() {
	// 1. Flags
	repo := flag.String("repo", "", "GitHub repository URL (e.g. github.com/fyne-io/examples/bugs)")
	targetOS := flag.String("os", "windows", "Target OS (linux, windows)")
	targetArch := flag.String("arch", "amd64", "Target Arch")
	url := flag.String("url", "", "Billder Service URL")
	token := flag.String("token", "", "Auth Token (optional)")
	flag.Parse()

	if *repo == "" || *url == "" {
		fmt.Println("❌ Error: --repo and --url are required")
		os.Exit(1)
	}

	// 2. Prepare Request
	payload := RequestPayload{
		RepoURL:    *repo,
		TargetOS:   *targetOS,
		TargetArch: *targetArch,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", *url, bytes.NewBuffer(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if *token != "" {
		req.Header.Set("X-Billder-Token", *token)
	}

	// 3. Connect
	client := &http.Client{Timeout: 0} // No timeout on client side, let server handle it
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Connection failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("❌ Server Error: %s\n", resp.Status)
		os.Exit(1)
	}

	fmt.Printf("🚀 Connected to Billder. Building %s for %s/%s...\n\n", *repo, *targetOS, *targetArch)

	// 4. Stream Processor (The "Hybrid" Loop)
	// We use bufio.Reader because it gives us fine-grained control over the buffer.
	reader := bufio.NewReader(resp.Body)
	var filename string

	for {
		// Read line by line
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil {
			// EOF or connection closed
			break
		}
		line := string(lineBytes)

		// Check for the "Switch Protocol" event
		if strings.HasPrefix(line, "event: binary_start") {
			// The next line contains "data: <filename>"
			dataLine, _ := reader.ReadString('\n')
			filename = strings.TrimSpace(strings.TrimPrefix(dataLine, "data:"))

			// Consume the mandatory empty line (\n) that ends the SSE block
			reader.ReadString('\n')

			// BREAK the loop. The rest of the stream is binary data.
			break
		}

		// Print standard log messages
		if strings.HasPrefix(line, "data:") {
			msg := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if msg != "" {
				fmt.Printf("✅ %s\n", msg)
			}
		}
	}

	// 5. Binary Download
	// If we exited the loop with a filename, the rest of the 'reader' buffer
	// plus the rest of 'resp.Body' is our file.
	if filename != "" {
		fmt.Printf("\n📦 Receiving artifact: %s...\n", filename)

		outFile, err := os.Create(filename)
		if err != nil {
			fmt.Printf("❌ Failed to create local file: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()

		// WriteTo writes the buffer from the Reader first, then reads the rest from underlying Body
		n, err := reader.WriteTo(outFile)
		if err != nil {
			fmt.Printf("❌ Download interrupted: %v\n", err)
		} else {
			duration := time.Since(start).Round(time.Second)
			fmt.Printf("✨ Success! Saved to %s (%d bytes) in %s.\n", filename, n, duration)
		}
	} else {
		fmt.Println("\n⚠️ Process finished, but no binary was received.")
	}
}
