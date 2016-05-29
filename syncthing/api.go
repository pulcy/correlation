package syncthing

import (
	"encoding/json"
	"io/ioutil"
	"net/url"
	"strconv"
	"time"

	"github.com/syncthing/syncthing/lib/config"
)

// Event holds full event data coming from Syncthing REST API
type Event struct {
	ID   int         `json:"id"`
	Time time.Time   `json:"time"`
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// GetMyID loads the ID of the current device
func (c *Client) GetMyID() (string, error) {
	response, err := c.httpGet("system/status")
	if err != nil {
		return "", maskAny(err)
	}
	raw, err := responseToBArray(response)
	if err != nil {
		return "", maskAny(err)
	}
	data := make(map[string]interface{})
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", maskAny(err)
	}
	return data["myID"].(string), nil
}

// GetConfig loads the current configuration
func (c *Client) GetConfig() (config.Configuration, error) {
	response, err := c.httpGet("system/config")
	if err != nil {
		return config.Configuration{}, maskAny(err)
	}
	raw, err := responseToBArray(response)
	if err != nil {
		return config.Configuration{}, maskAny(err)
	}
	config := config.Configuration{}
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, maskAny(err)
	}
	return config, nil
}

// SetConfig changes the current configuration
func (c *Client) SetConfig(cfg config.Configuration) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return maskAny(err)
	}
	if _, err := c.httpPost("system/config", string(body)); err != nil {
		return maskAny(err)
	}
	return nil
}

// IsConfigInSync returns true when the config is in sync, i.e. whether the running configuration is the same as that on disk.
func (c *Client) IsConfigInSync() (bool, error) {
	response, err := c.httpGet("system/config/insync")
	if err != nil {
		return false, maskAny(err)
	}
	raw, err := responseToBArray(response)
	if err != nil {
		return false, maskAny(err)
	}
	data := make(map[string]interface{})
	if err := json.Unmarshal(raw, &data); err != nil {
		return false, maskAny(err)
	}
	return data["configInSync"].(bool), nil
}

// Restart causes Syncthing to exit and restart.
func (c *Client) Restart() error {
	if _, err := c.httpPost("system/restart", ""); err != nil {
		return maskAny(err)
	}
	return nil
}

// Shutdown causes Syncthing to exit and not restart.
func (c *Client) Shutdown() error {
	if _, err := c.httpPost("system/shutdown", ""); err != nil {
		return maskAny(err)
	}
	return nil
}

// Scan causes Syncthing to scan the folder with given ID.
func (c *Client) Scan(folderID string, subs []string, delayScan time.Duration) error {
	data := url.Values{}
	data.Set("folder", folderID)
	for _, sub := range subs {
		data.Add("sub", sub)
	}
	delayScanS := (int)(delayScan.Seconds())
	if delayScanS > 0 {
		data.Set("next", strconv.Itoa(delayScanS))
	}
	resp, err := c.httpPost("db/scan?"+data.Encode(), "")
	if err != nil {
		return maskAny(err)
	}
	// Wait until scan finishes
	defer resp.Body.Close()
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return maskAny(err)
	}
	return nil
}

// GetEvents returns a list of events which happened in Syncthing since lastSeenID.
func (c *Client) GetEvents(lastSeenID int) ([]Event, error) {
	resp, err := c.httpGet("events?since=" + strconv.Itoa(lastSeenID))
	if err != nil {
		return nil, maskAny(err)
	}
	raw, err := responseToBArray(resp)
	if err != nil {
		return nil, maskAny(err)
	}
	var events []Event
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, maskAny(err)
	}
	return events, nil
}
