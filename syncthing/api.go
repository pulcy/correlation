package syncthing

import (
	"encoding/json"

	"github.com/syncthing/syncthing/lib/config"
)

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
