package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/docker/distribution/registry/api/errcode"
	"github.com/docker/distribution/registry/storage"
	"github.com/docker/distribution/registry/storage/driver"
	"github.com/gorilla/handlers"
)

const maximumReturnedEntries = 100

func catalogDispatcher(ctx *Context, r *http.Request) http.Handler {
	catalogHandler := &catalogHandler{
		Context: ctx,
	}

	return handlers.MethodHandler{
		"GET": http.HandlerFunc(catalogHandler.GetCatalog),
	}
}

type catalogHandler struct {
	*Context
}

type catalogAPIResponse struct {
	Repositories []string `json:"repositories"`
}

type reposList struct {
	Registry string   `json:"registry"`
	Repos    []string `json:"repos"`
}

func PostRepos(mpaasAddr string, registry string, repos []string) error {
	if mpaasAddr == "" {
		return nil
	}
	rep := &reposList{}
	rep.Registry = registry
	rep.Repos = repos

	data, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("Marshal failed,err:%s,struct:%v", err.Error(), rep)
	}
	resp, err := http.DefaultClient.Post(mpaasAddr, "application/json; charset=utf-8", strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("Post to:%s failed,err:%s", mpaasAddr, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respData, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("remote server return code:%d,data:%s", resp.StatusCode, string(respData))
	}
	return nil
}

func (ch *catalogHandler) GetCatalog(w http.ResponseWriter, r *http.Request) {
	var moreEntries = true

	q := r.URL.Query()
	lastEntry := q.Get("last")
	maxEntries, err := strconv.Atoi(q.Get("n"))
	if err != nil || maxEntries < 0 {
		maxEntries = maximumReturnedEntries
	}
	repos := make([]string, maxEntries)

	filled, err := ch.App.registry.Repositories(ch.Context, repos, lastEntry)
	_, pathNotFound := err.(driver.PathNotFoundError)

	if err == io.EOF || pathNotFound {
		moreEntries = false
	} else if err != nil && err != storage.ErrFinishedWalk {
		ch.Errors = append(ch.Errors, errcode.ErrorCodeUnknown.WithDetail(err))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// Add a link header if there are more entries to retrieve
	if moreEntries {
		lastEntry = repos[len(repos)-1]
		urlStr, err := createLinkEntry(r.URL.String(), maxEntries, lastEntry)
		if err != nil {
			ch.Errors = append(ch.Errors, errcode.ErrorCodeUnknown.WithDetail(err))
			return
		}
		w.Header().Set("Link", urlStr)
	}
	go func() {
		err := PostRepos(ch.App.Config.CatalogCallback, ch.App.Config.HTTP.Host, repos)
		if err != nil {
			fmt.Printf("err:%s\n", err.Error())
		}
	}()

	enc := json.NewEncoder(w)
	if err := enc.Encode(catalogAPIResponse{
		Repositories: repos[0:filled],
	}); err != nil {
		ch.Errors = append(ch.Errors, errcode.ErrorCodeUnknown.WithDetail(err))
		return
	}
}

// Use the original URL from the request to create a new URL for
// the link header
func createLinkEntry(origURL string, maxEntries int, lastEntry string) (string, error) {
	calledURL, err := url.Parse(origURL)
	if err != nil {
		return "", err
	}

	v := url.Values{}
	v.Add("n", strconv.Itoa(maxEntries))
	v.Add("last", lastEntry)

	calledURL.RawQuery = v.Encode()

	calledURL.Fragment = ""
	urlStr := fmt.Sprintf("<%s>; rel=\"next\"", calledURL.String())

	return urlStr, nil
}
