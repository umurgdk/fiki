// +build ignore
package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run main.go [variable name] [root-directory]")
		os.Exit(1)
	}

	varName := strings.Title(os.Args[1])

	root := os.Args[2]
	if root[len(root)-1] != '/' {
		root = root + "/"
	}

	var buf bytes.Buffer
	fmt.Fprintln(&buf, "package assets\n")
	fmt.Fprintf(&buf, "var %s = map[string][]byte {\n", varName)

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		content, err := ioutil.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		relativeName := strings.TrimPrefix(path, root)
		relativeName = strings.TrimSuffix(relativeName, filepath.Ext(path))
		fmt.Fprintf(&buf, "  \"%s\": []byte(\"", relativeName)
		for _, b := range content {
			fmt.Fprintf(&buf, "\\x%02x", b)
		}

		io.WriteString(&buf, "\"),\n")
		return nil
	})

	io.WriteString(&buf, "}\n")

	if err := os.MkdirAll("assets", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(1)
	}

	filename := strings.ToLower(varName)
	path := filepath.Join("assets", fmt.Sprintf("%s.go", filename))
	ioutil.WriteFile(path, buf.Bytes(), 0755)
}
