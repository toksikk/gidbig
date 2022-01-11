package wttrin

import (
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	baseURL = "https://wttr.in/"

	curlUserAgent = "curl/7.54.0"
)

var _httpClient *http.Client

// WeatherTextForToday returns today's weather for given place in single line plain text
func WeatherTextForToday(location string) (result string, err error) {
	return httpGet(baseURL + url.QueryEscape(location) + "?format=4")
}

func httpGet(url string) (result string, err error) {
	if _httpClient == nil {
		_httpClient = &http.Client{
			Transport: &http.Transport{
				Dial: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 300 * time.Second,
				}).Dial,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	}

	var req *http.Request

	if req, err = http.NewRequest("GET", url, nil); err == nil {

		req.Header.Set("User-Agent", curlUserAgent)

		var resp *http.Response
		resp, err = _httpClient.Do(req)

		if resp != nil {
			defer resp.Body.Close() // in case of http redirects
		}

		if err == nil {
			var body []byte
			if body, err = ioutil.ReadAll(resp.Body); err == nil {
				return string(body), nil
			}
		}
	}

	return "", err
}
