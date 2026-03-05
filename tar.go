package main

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
const offsetFile = "F:\\test\\readed"

func readTarFile(createDocument func(name string) (*document, error), processFile func(ctx context.Context, d *document, f io.Reader) error) {
	alphabetArr := map[string]struct{}{}
	for _, char := range alphabet {
		alphabetArr[string(char)] = struct{}{}
	}
	f, err := os.Open("F:\\test\\wikipedia-en-html.tar")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	tr := tar.NewReader(f)
	readed, err := os.ReadFile(offsetFile)
	if err != nil {
		log.Fatal(err)
	}
	offset, err := strconv.Atoi(string(readed))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("start offset = %d\n", offset)
	cnt := 0
	for {
		cnt++
		hdr, err := tr.Next()
		if err == io.EOF {
			// конец(край) архива
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		if cnt < offset {
			continue
		}
		offset++
		if offset%100 == 0 {
			fmt.Println("offset write", offset)
			err := os.WriteFile(offsetFile, []byte(strconv.Itoa(offset)), 0o777)
			if err != nil {
				log.Fatal(err)
			}
		}
		if !strings.HasPrefix(hdr.Name, "en/articles") {
			continue
		}
		arr := strings.SplitN(hdr.Name, "/", 6)
		if _, ok := alphabetArr[arr[2]]; !ok {
			continue
		}

		if _, ok := alphabetArr[arr[3]]; !ok {
			continue
		}

		if _, ok := alphabetArr[arr[4]]; !ok {
			continue
		}
		if strings.Contains(arr[5], "~") || strings.Contains(arr[2], "0") {
			continue
		}
		d, err := createDocument(hdr.Name)
		if err != nil {
			log.Fatal(err)
		}
		//	fmt.Printf("Файл: %s (%d bytes) %+v\n", hdr.Name, hdr.Size, arr)
		b, err := io.ReadAll(tr)
		if err != nil {
			log.Fatal(err)
		}

		plainText, err := stripHTMLTags(b)
		if err != nil {
			log.Fatal(err)
		}

		if err := processFile(context.Background(), d, strings.NewReader(plainText)); err != nil {
			log.Fatal(err)
		}
	}
	os.Exit(0)
}

func stripHTMLTags(htmlContent []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	var textBuilder strings.Builder
	var extractText func(*html.Node)
	extractText = func(n *html.Node) {
		if n.Type == html.TextNode {
			textBuilder.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractText(c)
		}
	}
	extractText(doc)
	return textBuilder.String(), nil
}
