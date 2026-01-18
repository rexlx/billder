package main

import (
	"encoding/json"
	"fmt"
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
	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func buildHandler(w http.ResponseWriter, r *http.Request) {
	// Setup SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	sendProgress := func(msg string) {
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
	}

	var payload RequestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		sendProgress("Error: Invalid JSON")
		return
	}
	if payload.TargetArch == "" {
		payload.TargetArch = "amd64"
	}

	// 1. Determine Compiler Environment
	//    This is the critical part for Fyne/CGo support.
	var env []string
	baseEnv := os.Environ() // Keep existing PATH, GOPATH, etc.

	switch payload.TargetOS {
	case "windows":
		// Use MinGW compiler for Windows targets
		// Note: 'x86_64-w64-mingw32-gcc' is standard on Debian for 64-bit Windows
		sendProgress("Configuring build for Windows (MinGW)...")
		env = append(baseEnv,
			"CGO_ENABLED=1",
			"GOOS=windows",
			"GOARCH="+payload.TargetArch,
			"CC=x86_64-w64-mingw32-gcc",
			"CXX=x86_64-w64-mingw32-g++",
		)
	case "linux":
		// Use standard GCC for Linux targets
		sendProgress("Configuring build for Linux (Native GCC)...")
		env = append(baseEnv,
			"CGO_ENABLED=1",
			"GOOS=linux",
			"GOARCH="+payload.TargetArch,
			"CC=gcc",
		)
	case "darwin":
		// MacOS Cross-compile with CGo is notoriously difficult on Linux
		// because of Apple SDK licensing. It usually requires 'osxcross'.
		sendProgress("Error: MacOS (darwin) cross-compilation with CGO is not supported in this container.")
		return
	default:
		sendProgress("Error: Unsupported OS")
		return
	}

	// 2. Create Workspace
	tmpDir, err := os.MkdirTemp("", "fyne-build-*")
	if err != nil {
		sendProgress("Error: Failed to create temp dir")
		return
	}
	defer os.RemoveAll(tmpDir)

	// 3. Clone
	sendProgress(fmt.Sprintf("Cloning %s...", payload.RepoURL))
	repoPath := filepath.Join(tmpDir, "src")
	if out, err := exec.Command("git", "clone", "https://"+payload.RepoURL, repoPath).CombinedOutput(); err != nil {
		log.Printf("Clone output: %s", out)
		sendProgress("Error: Git clone failed")
		return
	}

	// 4. Download Go Dependencies (Tidy)
	//    Fyne apps often need 'go mod tidy' ensuring all C-bound deps are resolved
	sendProgress("Resolving dependencies...")
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = repoPath
	tidyCmd.Env = env
	if out, err := tidyCmd.CombinedOutput(); err != nil {
		// Log but don't fail, sometimes tidy isn't strictly necessary if vendor exists
		log.Printf("Tidy warning: %s", out)
	}

	// 5. Build
	outputBinary := filepath.Join(tmpDir, "app")
	if payload.TargetOS == "windows" {
		outputBinary += ".exe"
	}

	sendProgress("Compiling (this may take 1-2 minutes for Fyne)...")

	// -trimpath makes the binary builds reproducible and removes local paths
	// -ldflags "-s -w -H=windowsgui" is recommmended for Fyne Windows apps to hide the console window
	buildArgs := []string{"build", "-trimpath", "-o", outputBinary}

	if payload.TargetOS == "windows" {
		// Hide the console window on Windows
		buildArgs = append(buildArgs, "-ldflags", "-s -w -H=windowsgui")
	} else {
		buildArgs = append(buildArgs, "-ldflags", "-s -w")
	}
	buildArgs = append(buildArgs, ".")

	buildCmd := exec.Command("go", buildArgs...)
	buildCmd.Dir = repoPath
	buildCmd.Env = env

	if out, err := buildCmd.CombinedOutput(); err != nil {
		// Capture the compiler error, which is crucial for CGo issues
		log.Printf("Build Error:\n%s", out)
		sendProgress("Error: Build failed. Check logs for C compiler errors.")
		// In a real app, you might send the last 5 lines of 'out' to the user here
		return
	}

	// 6. Success
	stat, _ := os.Stat(outputBinary)
	sendProgress(fmt.Sprintf("Success! Binary size: %.2f MB", float64(stat.Size())/1024/1024))

	// Close stream
	fmt.Fprintf(w, "event: close\ndata: close\n\n")
	flusher.Flush()
}
