// Copyright (c) 2016 Pulcy.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package syncthing

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

type ClientConfig struct {
	Endpoint           string
	APIKey             string
	InsecureSkipVerify bool
}

type Client struct {
	httpClient http.Client
	endpoint   string
	apikey     string
}

func NewClient(cfg ClientConfig) *Client {
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: cfg.InsecureSkipVerify,
			},
		},
	}
	client := &Client{
		httpClient: httpClient,
		endpoint:   cfg.Endpoint,
		apikey:     cfg.APIKey,
	}

	return client
}

func (client *Client) handleRequest(request *http.Request) (*http.Response, error) {
	if client.apikey != "" {
		request.Header.Set("X-API-Key", client.apikey)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, maskAny(err)
	}

	if response.StatusCode == 404 {
		return nil, maskAny(fmt.Errorf("Invalid endpoint or API call"))
	} else if response.StatusCode == 401 {
		return nil, maskAny(fmt.Errorf("Invalid username or password"))
	} else if response.StatusCode == 403 {
		return nil, maskAny(fmt.Errorf("Invalid API key"))
	} else if response.StatusCode != 200 {
		raw, err := responseToBArray(response)
		if err != nil {
			return nil, maskAny(err)
		}
		body := strings.TrimSpace(string(raw))
		if body != "" {
			return nil, maskAny(fmt.Errorf(body))
		}
		return nil, maskAny(fmt.Errorf("Unknown HTTP status returned: " + response.Status))
	}
	return response, nil
}

func (client *Client) httpGet(url string) (*http.Response, error) {
	request, err := http.NewRequest("GET", client.endpoint+"/rest/"+url, nil)
	if err != nil {
		return nil, maskAny(err)
	}
	resp, err := client.handleRequest(request)
	if err != nil {
		return nil, maskAny(err)
	}
	return resp, nil
}

func (client *Client) httpPost(url string, body string) (*http.Response, error) {
	request, err := http.NewRequest("POST", client.endpoint+"/rest/"+url, bytes.NewBufferString(body))
	if err != nil {
		return nil, maskAny(err)
	}
	resp, err := client.handleRequest(request)
	if err != nil {
		return nil, maskAny(err)
	}
	return resp, nil
}

func responseToBArray(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	bytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, maskAny(err)
	}
	return bytes, nil
}
