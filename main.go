package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/russross/blackfriday"
	"github.com/umurgdk/fiki/assets"
)

//go:generate go run embed.go Templates static/templates
//go:generate go run embed.go Public static/assets

var pages = make(map[string]string)
var templates = make(map[string]*template.Template)

func fetchTarball(username, repo string) error {
	tarballUrl := fmt.Sprintf("https://api.github.com/repos/%s/%s/tarball/master", username, repo)
	client := http.Client{}
	res, err := client.Get(tarballUrl)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	gzipReader, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}

	newPages := make(map[string]string)
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("error reading tar file: %v", err)
		}

		// Skip entries other than regular file
		// TODO: add support for symlinks
		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Skip files other than markdown
		if filepath.Ext(header.Name) != ".md" {
			continue
		}

		fileContent, err := ioutil.ReadAll(tarReader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v", header.Name, err)
			continue
		}

		// Skip the repo root folder
		parts := strings.SplitN(header.Name, "/", 2)
		if len(parts) < 2 {
			fmt.Printf("%v\n", parts)
		}
		filePath := parts[1]
		filePath = strings.TrimSuffix(filePath, ".md")

		// render markdown content
		html := blackfriday.Run(fileContent)
		newPages[filePath] = string(html)
		fmt.Fprintf(os.Stderr, "successfully processed %s\n", filePath)
	}

	pages = newPages
	return nil
}

func main() {
	println("Fetching tarball...")
	if err := fetchTarball("umurgdk", "wiki"); err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch tarball: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "%d pages cached\n", len(pages))
	for name, _ := range assets.Templates {
		templates[name] = template.Must(template.New(name).Parse(string(assets.Templates[name])))
	}

	http.HandleFunc("/", handler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[1:]
	if path == "" {
		path = "index"
	}

	page, ok := pages[path]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Add("Content-Type", "text/html;charset=utf-8")
	w.WriteHeader(http.StatusOK)
	err := templates["base"].Execute(w, struct {
		Topics []string
		Page   template.HTML
	}{
		Topics: []string{"linux", "programming", "japan"},
		Page:   template.HTML(page),
	})

	fmt.Fprintf(os.Stderr, "failed to run template 'base': %v", err)
}
