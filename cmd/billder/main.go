package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

type RequestPayload struct {
	RepoURL    string `json:"repo_url"`
	TargetOS   string `json:"target_os"`   // "linux" or "windows"
	TargetArch string `json:"target_arch"` // default "amd64"
}

func main() {
	http.HandleFunc("/build", buildHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Billder Server listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func buildHandler(w http.ResponseWriter, r *http.Request) {

	// 1. Method Check
	if r.Method != "POST" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 2. Auth Check (Simple Shared Secret)
	// Set the environment variable AUTH_TOKEN in Cloud Run
	expectedToken := os.Getenv("AUTH_TOKEN")
	if expectedToken != "" && r.Header.Get("X-Billder-Token") != expectedToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 3. Setup Streaming Headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Helper to send logs to client
	sendProgress := func(msg string) {
		// Clean newlines to avoid breaking SSE protocol
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
	}

	// 4. Parse Body (Limit to 4KB to prevent abuse)
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var payload RequestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		sendProgress("Error: Invalid JSON payload")
		return
	}
	log.Println("Received build request", payload)
	// Defaults
	if payload.TargetArch == "" {
		payload.TargetArch = "amd64"
	}

	sendProgress(fmt.Sprintf("Starting job for %s [%s/%s]", payload.RepoURL, payload.TargetOS, payload.TargetArch))

	// --- BUILD LOGIC ---

	// 5. Determine Compiler Environment
	var env []string
	baseEnv := os.Environ()

	switch payload.TargetOS {
	case "windows":
		// Use MinGW for Windows
		env = append(baseEnv,
			"CGO_ENABLED=1",
			"GOOS=windows",
			"GOARCH="+payload.TargetArch,
			"CC=x86_64-w64-mingw32-gcc",
			"CXX=x86_64-w64-mingw32-g++",
		)
	case "linux":
		// Use native GCC
		env = append(baseEnv,
			"CGO_ENABLED=1",
			"GOOS=linux",
			"GOARCH="+payload.TargetArch,
			"CC=gcc",
		)
	default:
		sendProgress("Error: Unsupported OS. Only 'linux' and 'windows' supported.")
		return
	}

	// 6. Create Temp Workspace
	tmpDir, err := os.MkdirTemp("", "billder-*")
	if err != nil {
		sendProgress("Error: Failed to create workspace")
		return
	}
	defer os.RemoveAll(tmpDir)

	// 7. Git Clone
	sendProgress("Step 1/3: Cloning repository...")
	repoPath := filepath.Join(tmpDir, "src")
	// Note: In production, validate payload.RepoURL to prevent command injection
	cloneCmd := exec.Command("git", "clone", "https://"+payload.RepoURL, repoPath)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		log.Printf("Clone Error: %s", out)
		sendProgress("Error: Git clone failed. Is the URL correct?")
		return
	}
	dirContents, _ := os.ReadDir(repoPath)
	if len(dirContents) == 0 {
		sendProgress("Error: Repository is empty.")
		return
	}

	log.Println("Repository cloned to", repoPath, dirContents)

	// 8. Go Mod Tidy
	sendProgress("Step 2/3: Resolving dependencies...")
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = repoPath
	tidyCmd.Env = env
	_ = tidyCmd.Run() // Ignore errors here, just a best effort cleanup

	// 9. Go Build
	sendProgress("Step 3/3: Compiling...")
	outputBinary := filepath.Join(tmpDir, "app")
	if payload.TargetOS == "windows" {
		outputBinary += ".exe"
	}

	buildArgs := []string{"build", "-trimpath", "-o", outputBinary}
	if payload.TargetOS == "windows" {
		// -H=windowsgui hides the console window on Windows
		buildArgs = append(buildArgs, "-ldflags", "-s -w -H=windowsgui")
	} else {
		buildArgs = append(buildArgs, "-ldflags", "-s -w")
	}
	buildArgs = append(buildArgs, ".")

	buildCmd := exec.Command("go", buildArgs...)
	buildCmd.Dir = repoPath
	buildCmd.Env = env
	log.Println("Running build command:", buildCmd.Args)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		log.Printf("Build Output: %s", out)
		sendProgress("Error: Compilation failed.")
		// Optionally send the last few lines of 'out' to the user
		return
	}

	// 10. Handover Strategy (Stream the file)
	stat, _ := os.Stat(outputBinary)
	fileSizeMB := float64(stat.Size()) / 1024 / 1024
	log.Printf("Binary built successfully: %s (%.2f MB)", outputBinary, fileSizeMB)
	sendProgress(fmt.Sprintf("Build Successful! Artifact size: %.2f MB", fileSizeMB))

	// Open the binary file
	f, err := os.Open(outputBinary)
	if err != nil {
		sendProgress("Error: Could not open built artifact")
		return
	}
	defer f.Close()

	// SIGNAL: Tell client to switch to binary mode
	// We send the filename in the 'data' field
	fmt.Fprintf(w, "event: binary_start\ndata: %s\n\n", filepath.Base(outputBinary))
	flusher.Flush()

	// STREAM: Copy raw bytes to the response body
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("Streaming error: %v", err)
	}
}
