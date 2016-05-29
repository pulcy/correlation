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

package backend

import (
	"fmt"
	"sort"
	"strings"
)

type Backend interface {
	// Watch for changes in the backend and return where there is a change.
	Watch() error

	// Load all registered devices
	Get() (DeviceRegistrations, error)

	// Announce the given device that can be found at the given address
	Announce(deviceID, address string)

	// UnAnnounce removes the current announcement
	UnAnnounce()
}

type DeviceRegistration struct {
	ID   string // Unique ID of the device
	IP   string // IP address to connect to to reach the device
	Port int    // Port to connect to to reach the device
}

func (d DeviceRegistration) FullString() string {
	return fmt.Sprintf("%s-%s-%d", d.ID, d.IP, d.Port)
}

type DeviceRegistrations []DeviceRegistration

func (list DeviceRegistrations) FullString() string {
	if len(list) == 0 {
		return ""
	}
	slist := []string{}
	for _, si := range list {
		slist = append(slist, si.FullString())
	}
	sort.Strings(slist)
	return "[" + strings.Join(slist, ",") + "]"
}

func (list DeviceRegistrations) Sort() {
	sort.Sort(list)
}

// Len is the number of elements in the collection.
func (list DeviceRegistrations) Len() int {
	return len(list)
}

// Less reports whether the element with
// index i should sort before the element with index j.
func (list DeviceRegistrations) Less(i, j int) bool {
	a := list[i].FullString()
	b := list[j].FullString()
	return strings.Compare(a, b) < 0
}

// Swap swaps the elements with indexes i and j.
func (list DeviceRegistrations) Swap(i, j int) {
	list[i], list[j] = list[j], list[i]
}
