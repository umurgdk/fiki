package main

import (
	"archive/tar"
	"bytes"
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
	"time"

	"github.com/russross/blackfriday"
	"github.com/umurgdk/fiki/assets"
)

//go:generate go run embed.go Templates static/templates
//go:generate go run embed.go Public static/assets

const host = "0.0.0.0"
const port = 8080

var hierarchy = make(map[string][]string)

var pageTree *TreeNode
var pages = make(map[string]string)
var templates = make(map[string]*template.Template)
var topics []string

type TreeNode struct {
	Name     string
	Page     bool
	Path     string
	Children map[string]*TreeNode
}

func newTreeNode(name string, path string, page bool) *TreeNode {
	return &TreeNode{
		Name:     name,
		Path:     path,
		Page:     page,
		Children: make(map[string]*TreeNode),
	}
}

var local = flag.String("local", "", "Path to a local directory to serve from it instead of a git repository")

var tmplFuncMap = template.FuncMap{
	"title": strings.Title,
	"base":  filepath.Base,
	"isActive": func(path string) bool {
		return false
	},
}

func withLog(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler(w, r)
		log.Printf("%s %s took: %v\n", r.Method, r.URL.Path, time.Since(start))
	}
}

func main() {
	flag.Parse()

	log.Println("compiling templates...")
	for name, _ := range assets.Templates {
		tmpl, err := template.New(name).Funcs(tmplFuncMap).Parse(string(assets.Templates[name]))
		templates[name] = template.Must(tmpl, err)
	}

	pageTree = newTreeNode("root", "", false)
	if *local == "" {
		log.Println("fetching wiki tarball...")
		if err := fetchTarball("umurgdk", "wiki"); err != nil {
			log.Printf("failed to fetch tarball: %v\n", err)
			os.Exit(1)
		}
	} else {
		log.Printf("reading local directory: %s\n", *local)
		if err := readLocalDirectory(*local); err != nil {
			log.Printf("failed to read local directory: %v\n", err)
			os.Exit(1)
		}
	}

	log.Printf("%d pages cached\n", len(pages))
	log.Printf("topcis: %v\n", topics)

	http.HandleFunc("/_githook", withLog(webhookHandler))
	http.HandleFunc("/theme/", stripPrefix("/theme", withLog(themeHandler)))
	http.HandleFunc("/", withLog(pageHandler))

	hostPort := fmt.Sprintf("%s:%d", host, port)
	log.Println("start listening at ", hostPort)

	log.Fatal(http.ListenAndServe(hostPort, nil))
}

func pageHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[1:]
	if path == "" {
		path = "index"
	}

	page, pageFound := pages[path]
	children, childrenFound := hierarchy[path]
	if !pageFound {
		page, pageFound = pages[filepath.Join(path, "index")]
		if !pageFound && !childrenFound {
			http.NotFound(w, r)
			return
		}

		var buf bytes.Buffer
		err := templates["index.tmpl"].Execute(&buf, struct {
			Title    string
			Page     template.HTML
			Path     string
			Children []string
			Topics   []string
		}{
			Title:    filepath.Base(path),
			Page:     template.HTML(page),
			Path:     path,
			Children: children,
			Topics:   topics,
		})

		if err != nil {
			log.Printf("error: failed to run index template: %v\n", err)

			w.Header().Add("Content-Type", "plain/text")
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "Internal server error")
			return
		}

		page = buf.String()
	}

	var breadcrumb []string
	if strings.ContainsRune(path, '/') {
		b := strings.Split(filepath.Dir(path), "/")
		for i, _ := range b {
			breadcrumb = append(breadcrumb, strings.Join(b[:i+1], "/"))
		}
	}

	w.Header().Add("Content-Type", "text/html;charset=utf-8")
	w.WriteHeader(http.StatusOK)
	err := templates["base.tmpl"].Funcs(template.FuncMap{
		"isActive": func(p string) bool {
			return p == path
		},
	}).Execute(w, struct {
		Topics     []string
		Title      string
		Page       template.HTML
		Breadcrumb []string
		Tree       *TreeNode
	}{
		Topics:     topics,
		Title:      filepath.Base(path),
		Page:       template.HTML(page),
		Breadcrumb: breadcrumb,
		Tree:       pageTree,
	})

	if err != nil {
		log.Printf("error: failed to run template 'base': %v\n", err)
	}
}

func themeHandler(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("error: failed to send theme file: %v\n", err)
	}
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	go fetchTarball("umurgdk", "wiki")
}

func readLocalDirectory(root string) error {
	filepath.Walk(root, func(fpath string, info os.FileInfo, err error) error {
		if fpath == root {
			return nil
		}

		// 1. Calculate the relative path
		relPath := strings.TrimPrefix(fpath, root)[1:]
		if relPath[0] == '.' {
			return nil
		}

		// TODO: Do proper reading
		// 2. If current entry is a directory `info.IsDir()` add into to hierarchy
		if info.IsDir() {
			if !strings.ContainsRune(relPath, '/') {
				topics = append(topics, relPath)
			}

			dir := filepath.Dir(relPath)
			if dir != "." {
				hierarchy[dir] = append(hierarchy[dir], filepath.Base(relPath))
			}

			return nil
		}

		// 3. If file have a markdown extension run the markdown processor and put it into pages
		if filepath.Ext(relPath) != ".md" {
			return nil
		}

		content, err := ioutil.ReadFile(fpath)
		if err != nil {
			return err
		}

		pagePath := strings.TrimSuffix(relPath, filepath.Ext(relPath))
		html := blackfriday.Run(content)
		pages[pagePath] = string(html)

		// 4. If current entry is a file and it isn't in the root directory add entrys path to associated hierarchy
		fileName := filepath.Base(pagePath)
		if fileName != "index" {
			dir := filepath.Dir(relPath)
			hierarchy[dir] = append(hierarchy[dir], fileName)
			treeAppend(dir, fileName)
		}

		return nil
	})

	return nil
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

		// Skip the repo root folder
		parts := strings.SplitN(header.Name, "/", 2)
		if len(parts) != 2 {
			continue
		}

		entryPath := parts[1]
		entryPath = strings.TrimSuffix(entryPath, ".md")

		// If the entry is a directory add it to the hierarchy collection, in
		// case of it is both directory and located in the root, add it to the
		// topics collections
		if header.Typeflag == tar.TypeDir {
			dir := strings.TrimSuffix(entryPath, "/")
			if dir == "" {
				continue
			}

			if !strings.ContainsRune(dir, '/') {
				topics = append(topics, dir)
			} else {
				parentDir := filepath.Dir(dir)
				if parentDir != "." {
					hierarchy[parentDir] = append(hierarchy[parentDir], filepath.Base(dir))
				}
			}

			continue
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
			log.Printf("error reading %s: %v", header.Name, err)
			continue
		}

		// render markdown content
		html := blackfriday.Run(fileContent)
		newPages[entryPath] = string(html)

		fileName := filepath.Base(entryPath)
		if fileName != "index" {
			dir := filepath.Dir(entryPath)
			hierarchy[dir] = append(hierarchy[dir], filepath.Base(entryPath))
			treeAppend(dir, filepath.Base(entryPath))
		}
	}

	pages = newPages
	return nil
}

func treeAppend(path, name string) {
	parts := strings.Split(path, "/")
	node := pageTree
	for i, dir := range parts {
		child, ok := node.Children[dir]
		if !ok {
			childPath := filepath.Join(parts[:i+1]...)
			node.Children[dir] = newTreeNode(dir, childPath, false)
			node = node.Children[dir]
			continue
		}

		node = child
	}

	fullPath := filepath.Join(path, name)
	node.Children[name] = newTreeNode(name, fullPath, true)
}

func stripPrefix(prefix string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		handler(w, r)
	}
}
