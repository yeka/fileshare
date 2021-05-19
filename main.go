package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	DisableDirectoryListing bool
	DontRemoveOnError bool
	BasePath string
}

//go:embed web
var web embed.FS

var config = Config{}

func main() {
	mux := http.NewServeMux()
	fsys, err := fs.Sub(web, "web")
	if err != nil {
		panic(err)
	}

	mux.Handle("/", http.FileServer(http.FS(fsys)))
	mux.HandleFunc("/list", handleList)
	mux.HandleFunc("/upload", handleUpload)
	log.Println("Server ready")
	log.Println(http.ListenAndServe(":8123", mux))
}

func handleList(w http.ResponseWriter, r *http.Request) {
	if config.DisableDirectoryListing {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
		return
	}
	path, err := validatePath(config.BasePath, r.URL.Query().Get("path"))
	if err != nil {
		responseError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !strings.HasSuffix(path, "/") {
		// Download
		log.Println("Downloading " + path)
		cd := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)})
		w.Header().Set("Content-Disposition", cd)
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, path)
		return
	}

	fsys, err := os.ReadDir(path)
	if err != nil {
		responseError(w, http.StatusBadRequest, err.Error())
		return
	}
	paths := make([]string, 0)
	for _, f := range fsys {
		if strings.HasPrefix(f.Name(), ".") {
			continue
		}
		name := f.Name()
		if f.IsDir() {
			name += "/"
		}
		paths = append(paths, name)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("[" + strings.Join(paths, ",") + "]"))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	path, err := validatePath(config.BasePath, r.URL.Query().Get("path"))
	if err != nil {
		responseError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !strings.HasSuffix(path, "/") {
		responseError(w, http.StatusBadRequest, "not a directory")
		return
	}

	mp, err := r.MultipartReader()
	if err != nil {
		responseError(w, http.StatusBadRequest, err.Error())
		return
	}

	for {
		part, err := mp.NextPart()
		if err == io.EOF || part == nil {
			break
		}

		if part.FormName() == "myFile" {
			if err := upload(path, part); err != nil {
				responseError(w, http.StatusBadRequest, "upload failed: "+err.Error())
			}
			_ = part.Close()
			return
		}
	}

	responseError(w, http.StatusBadRequest, "unknown payload")
}

func validatePath(base, p string) (string, error) {
	p = filepath.Clean(p)
	if p != "." && p != "" {
		ss := strings.Split(p, "/")
		for _, s := range ss {
			if strings.HasPrefix(s, ".") { // this includes ..
				return "", errors.New("invalid path: " + s)
			}
		}
	}

	p = filepath.Join(base, p)
	fi, err := os.Stat(p)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}

		return "", err
	}

	if fi.IsDir() {
		p += "/"
	}
	return p, nil
}

func responseError(w http.ResponseWriter, statusCode int, msg string) {
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(msg))
}

func upload(path string, part *multipart.Part) error {
	fileName := part.FileName()
	ext := filepath.Ext(fileName)
	file := filepath.Join(path, fileName)

	// check if file exists
	i := 0
	for {
		_, err := os.Stat(file)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}

		i++
		file = file[:len(file)-len(ext)] + " (" + strconv.Itoa(i) + ")" + ext
	}

	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	n, err := io.Copy(f, part)
	if err != nil && !config.DontRemoveOnError {
		// should be optional
		_ = os.Remove(file)
	}
	fmt.Println(file, n, err)
	return err
}
