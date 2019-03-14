package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

var allowedDomain string
var alreadyVisitedUrls = map[string]bool{}
var errorsList = make([]string, 0)
var lock = sync.RWMutex{} // Lock mechanism used to work with concurrent map read and writes.
var staticFilesList = map[string][]string{}
var urlsList = make([]string, 0)
var urlsListIndex = map[string]int{}
var urlsLinksTo = map[int]map[int]bool{} //
var waitGroup sync.WaitGroup

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

func checkForCSSReference(parentUrl string, token html.Token) {
	isStylesheet := false
	stylesheet := ""
	breakLoop := false
	for _, attribute := range token.Attr {
		switch attribute.Key {
		case "href":
			stylesheet = getProcessedUrl(parentUrl, attribute.Val)
			if attribute.Val != "" {
				addStaticFile(parentUrl, getProcessedUrl(parentUrl, attribute.Val))
			}
			break
		case "rel":
			if attribute.Val == "stylesheet" {
				isStylesheet = true
				break
			}
			breakLoop = true
			break
		default:
			break
		}
		if isStylesheet && stylesheet != "" {
			addStaticFile(parentUrl, stylesheet)
			break
		}
		if breakLoop {
			break
		}
	}
}

func checkForImageReference(parentUrl string, token html.Token) {
	for _, attribute := range token.Attr {
		if attribute.Key == "src" {
			addStaticFile(parentUrl, getProcessedUrl(parentUrl, attribute.Val))
			break
		}
	}
}

func checkForJavaScriptReference(parentUrl string, token html.Token) {
	for _, attribute := range token.Attr {
		if attribute.Key == "src" && attribute.Val != "" {
			addStaticFile(parentUrl, getProcessedUrl(parentUrl, attribute.Val))
			break
		}
	}
}

func checkForUrlReference(parentUrl string, token html.Token) {
	for _, attribute := range token.Attr {
		if attribute.Key == "href" {
			link := getProcessedUrl(parentUrl, attribute.Val)
			waitGroup.Add(1)
			go func() {
				visited := visitPage(link)
				if visited {
					callerUrlIndex := addOrGetIndexForUrl(parentUrl)
					currentUrlIndex := addOrGetIndexForUrl(link)
					lock.Lock()
					defer lock.Unlock()
					urlsLinksTo[callerUrlIndex][currentUrlIndex] = true
				}
				waitGroup.Done()
			}()
			break
		}
	}
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
	// TODO: remove IDEA run hack.
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

	parsedUrl, err := url.Parse(inputUrl)
	if err != nil {
		fmt.Println(err)
		return
	}

	allowedDomain = parsedUrl.Hostname()

	waitGroup.Add(1)
	go func() {
		visitPage(inputUrl)
		waitGroup.Done()
	}()

	waitGroup.Wait()

	for _, singleError := range errorsList {
		fmt.Println(singleError)
	}

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

func getProcessedUrl(parentUrl string, rawUrl string) string {
	if strings.HasPrefix(rawUrl, "#") {
		return ""
	}
	if parentUrl != "" {
		relativeUrl, err := url.Parse(rawUrl)
		if err != nil {
			errorsList = append(errorsList, "Relative URL: "+rawUrl+" parsing failed with error:"+err.Error())
			return ""
		}
		baseUrl, err := url.Parse(parentUrl)
		if err != nil {
			errorsList = append(errorsList, "Base URL: "+parentUrl+" parsing failed with error:"+err.Error())
			return ""
		}
		return removeTrailingSlash(baseUrl.ResolveReference(relativeUrl).String())
	}
	return removeTrailingSlash(rawUrl)
}

func getUrlWithoutProtocol(urlWithProtocol string) string {
	parsedUrl, err := url.Parse(urlWithProtocol)
	if err != nil {
		errorsList = append(errorsList, "URL: "+urlWithProtocol+" parsing failed with error:"+err.Error())
		return ""
	}
	urlWithoutProtocol := strings.TrimLeft(parsedUrl.String(), parsedUrl.Scheme)
	return urlWithoutProtocol
}

func parseHtml(parentUrl string, pageDataReader io.ReadCloser) {
	htmlTokens := html.NewTokenizer(pageDataReader)
	breakLoop := false
	for {
		tokenType := htmlTokens.Next()
		switch tokenType {
		case html.ErrorToken:
			breakLoop = true // Finish parsing at document end.
			break
		case html.SelfClosingTagToken, html.StartTagToken:
			token := htmlTokens.Token()
			switch token.Data {
			case "a":
				checkForUrlReference(parentUrl, token)
				break
			case "img":
				checkForImageReference(parentUrl, token)
				break
			case "link":
				checkForCSSReference(parentUrl, token)
				break
			case "script":
				checkForJavaScriptReference(parentUrl, token)
				break
			default:
				break
			}
		}
		if breakLoop {
			break
		}
	}
}

func removeTrailingSlash(data string) string {
	return strings.TrimRight(data, "/")
}

func visitPage(pageUrl string) bool {
	processedUrl := getProcessedUrl("", pageUrl)
	urlWithoutProtocol := getUrlWithoutProtocol(processedUrl)
	lock.Lock()
	if _, ok := alreadyVisitedUrls[urlWithoutProtocol]; !ok { // Check for the URl without the protocol (http:// or https://) to visit the URL just once.
		alreadyVisitedUrls[urlWithoutProtocol] = true
		lock.Unlock()
		parsedUrl, err := url.Parse(pageUrl)
		if err != nil {
			fmt.Println(err)
			return false
		}
		if parsedUrl.Hostname() == allowedDomain {
			headResponse, err := http.Head(processedUrl)
			if err != nil {
				errorsList = append(errorsList, "Request URL (HEAD): "+headResponse.Request.URL.String()+" failed with error:"+err.Error())
				return false
			}
			if strings.Index(headResponse.Header.Get("Content-Type"), "html") > -1 {
				getResponse, err := http.Get(processedUrl)
				if err != nil {
					errorsList = append(errorsList, "Request URL (GET): "+getResponse.Request.URL.String()+" failed with error:"+err.Error())
					return false
				}
				defer getResponse.Body.Close()
				if getResponse.StatusCode != 200 {
					errorsList = append(errorsList, "Request URL (GET): "+getResponse.Request.URL.String()+" failed with status code:"+strconv.Itoa(getResponse.StatusCode))
					return false
				}
				fmt.Println("Visiting", processedUrl)
				addOrGetIndexForUrl(processedUrl)
				parseHtml(processedUrl, getResponse.Body)
				return true
			}
			return false
		}
		return false
	}
	if _, ok := urlsListIndex[processedUrl]; ok {
		lock.Unlock()
		return true
	}
	lock.Unlock()
	return false
}
