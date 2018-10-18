package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/russross/blackfriday"
	"github.com/umurgdk/fiki/assets"
)

//go:generate go run embed.go Templates static/templates
//go:generate go run embed.go Public static/assets

const host = "localhost"
const port = 8080

var hierarchy = make(map[string][]string)

var pageTree = make(map[string]string)
var pages = make(map[string]string)
var templates = make(map[string]*template.Template)

var local = flag.String("local", "", "Path to a local directory to serve from it instead of a git repository")

func main() {
	flag.Parse()

	println("compiling templates...")
	for name, _ := range assets.Templates {
		templates[name] = template.Must(template.New(name).Parse(string(assets.Templates[name])))
	}

	if *local == "" {
		println("fetching wiki tarball...")
		if err := fetchTarball("umurgdk", "wiki"); err != nil {
			fmt.Fprintf(os.Stderr, "failed to fetch tarball: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "reading local directory: %s\n", *local)
		if err := readLocalDirectory(*local); err != nil {
			fmt.Fprintf(os.Stderr, "failed to read local directory: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "%d pages cached\n", len(pages))

	http.HandleFunc("/theme/", stripPrefix("/theme", themeHandler))
	http.HandleFunc("/", handler)

	hostPort := fmt.Sprintf("%s:%d", host, port)
	println("start listening at ", hostPort)

	log.Fatal(http.ListenAndServe(hostPort, nil))
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
	err := templates["base.tmpl"].Execute(w, struct {
		Topics []string
		Page   template.HTML
	}{
		Topics: []string{"linux", "programming", "japan"},
		Page:   template.HTML(page),
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to run template 'base': %v", err)
	}
}

func themeHandler(w http.ResponseWriter, r *http.Request) {
	println("serving theme file: ", r.URL.Path)
	if len(r.URL.Path) <= 2 {
		http.NotFound(w, r)
		return
	}

	file, ok := assets.Public[r.URL.Path[1:]]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Add("Content-Type", mime.TypeByExtension(filepath.Ext(r.URL.Path)))
	w.WriteHeader(200)
	_, err := w.Write(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to send theme file: %v\n", err)
	}
}

func readLocalDirectory(path) error {
	filepath.Walk(path, func(fpath string, info os.FileInfo, err error) error {
		if fpath == path {
			return nil
		}

		// TODO: Do proper reading
		// 1. Calculate the relative path
		// 2. If current entry is a directory `info.IsDir()` add into to hierarchy
		// 3. If file have a markdown extension run the markdown processor and put it into pages
		// 4. If current entry is a file and it isn't in the root directory add entrys path to associated hierarchy

		if filepath.Ext(path) != ".md" {
			return nil
		}

		content, err := ioutil.ReadFile(path)
	})
}

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

func stripPrefix(prefix string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		handler(w, r)
	}
}
