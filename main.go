package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/gocolly/colly"
)

var lock = sync.RWMutex{}
var staticFilesList = map[string][]string{}
var urlsList = make([]string, 0)
var urlsListIndex = map[string]int{}
var urlsLinksTo = map[int]map[int]bool{}

type UrlObject struct {
	Id          int
	Address     string
	StaticFiles []string
	LinksTo     []int
}

func addOrGetIndexForUrl(urlString string) int {
	lock.Lock()
	defer lock.Unlock()
	currentUrl := removeTrailingSlash(urlString)
	var urlIndex int
	if val, ok := urlsListIndex[currentUrl]; ok {
		urlIndex = val
	} else {
		urlsList = append(urlsList, currentUrl)
		urlIndex = len(urlsList) - 1
		urlsListIndex[currentUrl] = urlIndex
		urlsLinksTo[urlIndex] = map[int]bool{}
		staticFilesList[currentUrl] = make([]string, 0)
	}
	return urlIndex
}

func addStaticFile(currentUrl string, staticFileUrl string) {
	lock.Lock()
	defer lock.Unlock()
	currentUrl = removeTrailingSlash(currentUrl)
	staticFileUrl = removeTrailingSlash(staticFileUrl)
	staticFilesList[currentUrl] = append(staticFilesList[currentUrl], staticFileUrl)
}

func createSliceFromMapKeys(data map[int]bool) []int {
	slice := make([]int, 0)
	for key := range data {
		slice = append(slice, key)
	}
	sort.Ints(slice)
	return slice
}

func main() {
	args := os.Args
	if len(args) != 2 && len(args) != 3 {
		fmt.Println("invalid args")
		return
	}

	// IDEA run hack.
	if len(args) == 3 {
		args = args[1:3]
	}

	inputUrl := args[0]
	outputFilename := args[1]

	collector := colly.NewCollector(
		colly.Async(true),
	)

	parsedUrl, err := url.Parse(inputUrl)
	if err != nil {
		fmt.Println(err)
		return
	}

	collector.AllowedDomains = []string{parsedUrl.Hostname()}

	collector.OnHTML("a", func(htmlElement *colly.HTMLElement) {
		link := removeTrailingSlash(htmlElement.Request.AbsoluteURL(htmlElement.Attr("href")))
		err := htmlElement.Request.Visit(link)
		switch err {
		case colly.ErrForbiddenDomain, colly.ErrMissingURL:
			break
		case colly.ErrAlreadyVisited, nil:
			linkParsedUrl, err := url.Parse(link)
			if err != nil {
				fmt.Println(err)
				return
			}
			if linkParsedUrl.Hostname() == parsedUrl.Hostname() {
				callerUrlIndex := addOrGetIndexForUrl(htmlElement.Request.AbsoluteURL(htmlElement.Request.URL.String()))
				currentUrlIndex := addOrGetIndexForUrl(link)
				lock.Lock()
				defer lock.Unlock()
				urlsLinksTo[callerUrlIndex][currentUrlIndex] = true
			}
			break
		default:
			fmt.Println("Request URL:", htmlElement.Attr("href"), "failed with error:", err)
			break
		}
	})

	// Image references.
	collector.OnHTML("img", func(htmlElement *colly.HTMLElement) {
		addStaticFile(htmlElement.Request.URL.String(), htmlElement.Request.AbsoluteURL(htmlElement.Attr("src")))
	})

	// CSS references.
	collector.OnHTML("link", func(htmlElement *colly.HTMLElement) {
		if htmlElement.Attr("rel") == "stylesheet" {
			addStaticFile(htmlElement.Request.URL.String(), htmlElement.Request.AbsoluteURL(htmlElement.Attr("href")))
		}
	})

	// JavaScript references.
	collector.OnHTML("script", func(htmlElement *colly.HTMLElement) {
		src := htmlElement.Attr("src")
		if src != "" {
			addStaticFile(htmlElement.Request.URL.String(), htmlElement.Request.AbsoluteURL(htmlElement.Attr("src")))
		}
	})

	collector.OnResponse(func(response *colly.Response) {
		if strings.Index(response.Headers.Get("Content-Type"), "html") > -1 {
			fmt.Println("Visiting", response.Request.AbsoluteURL(response.Request.URL.String()))
			addOrGetIndexForUrl(response.Request.AbsoluteURL(response.Request.URL.String()))
		}
	})

	collector.OnError(func(response *colly.Response, err error) {
		fmt.Println("Request URL:", response.Request.URL, "failed with error:", err)
	})

	err = collector.Visit(inputUrl)
	if err != nil {
		fmt.Println(err)
		return
	}

	collector.Wait()

	var jsonData []UrlObject
	for _, urlString := range urlsList {
		urlIndex := urlsListIndex[urlString]
		urlObject := UrlObject{
			Id:          urlIndex,
			Address:     urlString,
			StaticFiles: staticFilesList[urlString],
			LinksTo:     createSliceFromMapKeys(urlsLinksTo[urlIndex]),
		}
		jsonData = append(jsonData, urlObject)
	}

	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		fmt.Println(err)
		return
	}

	err = ioutil.WriteFile(outputFilename, jsonBytes, 0644)
	if err != nil {
		fmt.Println(err)
	}
}

func removeTrailingSlash(data string) string {
	return strings.TrimRight(data, "/")
}
