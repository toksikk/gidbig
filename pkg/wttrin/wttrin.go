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

// WeatherForToday returns today's weather for given place in byte array
func WeatherForToday(location string) (result []byte, err error) {
	return httpGet(baseURL + url.QueryEscape(location))
}

func httpGet(url string) (result []byte, err error) {
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
				return body, nil
			}
		}
	}

	return make([]byte, 0), err
}
