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
//
// This code is heaviliy inspired by https://github.com/syncthing/syncthing-inotify.
// The main difference if that in here we only care about a single folder.

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/op/go-logging"
	"github.com/syncthing/syncthing/lib/ignore"
	"github.com/zillode/notify"

	"github.com/pulcy/correlation/syncthing"
)

// Pattern holds ignored path and a boolean which value is false when we should use the pattern in exclude mode
type Pattern struct {
	match   *regexp.Regexp
	include bool
}

// STEvent holds simplified data for Syncthing event. Path can be empty in the case of event.type="RemoteIndexUpdated"
type STEvent struct {
	Path     string
	Finished bool
}

type folderSlice []string

type progressTime struct {
	fsEvent bool // true - event was triggered by filesystem, false - by Syncthing
	time    time.Time
}

func (fs *folderSlice) String() string {
	return fmt.Sprint(*fs)
}
func (fs *folderSlice) Set(value string) error {
	for _, f := range strings.Split(value, ",") {
		*fs = append(*fs, f)
	}
	return nil
}

// HTTP Debounce
var (
	debounceTimeout   = 500 * time.Millisecond
	configSyncTimeout = 5 * time.Second
	fsEventTimeout    = 5 * time.Second
	dirVsFiles        = 128
	maxFiles          = 512
)

const (
	pathSeparator = string(os.PathSeparator)
)

// Watcher maintaints the state of a single inotify watcher.
type Watcher struct {
	log          *logging.Logger
	syncClient   *syncthing.Client
	folderID     string
	folderPath   string
	stop         chan int
	closed       bool
	ignorePaths  []string
	watchFolders folderSlice
	skipFolders  folderSlice
	delayScan    time.Duration
	stInput      chan STEvent
	fsInput      chan string
}

// NewWatcher creates a new watcher.
func NewWatcher(log *logging.Logger, syncClient *syncthing.Client, folderID, folderPath string) *Watcher {
	return &Watcher{
		log:         log,
		syncClient:  syncClient,
		folderID:    folderID,
		folderPath:  folderPath,
		stop:        make(chan int),
		ignorePaths: []string{".stversions", ".syncthing.", "~syncthing~"},
		delayScan:   time.Second * 3600,
		stInput:     make(chan STEvent),
		fsInput:     make(chan string),
	}
}

// Run starts all gouroutines and waits until a message is in channel stop.
func (w *Watcher) Run() int {
	// Attempt to increase the limit on number of open files to the maximum allowed.
	MaximizeOpenFileLimit()

	go w.watchFolder()
	go w.watchSTEvents()

	code := <-w.stop
	return code
}

// Close stops the watcher
func (w *Watcher) Close() {
	w.closed = true
	w.stop <- 0
}

// watchFolder installs inotify watcher for a folder, launches
// goroutine which receives changed items. It never exits.
func (w *Watcher) watchFolder() {
	ignores := ignore.New(false)
	w.log.Debugf("getting ignore patterns for %s", w.folderPath)
	ignores.Load(filepath.Join(w.folderPath, ".stignore"))

	c := make(chan notify.EventInfo, maxFiles)
	ignoreTest := func(absolutePath string) bool {
		relPath := relativePath(absolutePath, w.folderPath)
		return ignores.Match(relPath).IsIgnored()
	}
	notify.SetDoNotWatch(ignoreTest)
	if err := notify.Watch(filepath.Join(w.folderPath, "..."), c, notify.All); err != nil {
		if strings.Contains(err.Error(), "too many open files") || strings.Contains(err.Error(), "no space left on device") {
			w.log.Warningf("Failed to install inotify handler for %s. Please increase inotify limits, see http://bit.ly/1PxkdUC for more information.", w.folderID)
			return
		} else {
			w.log.Warningf("Failed to install inotify handler for %s: %#v", w.folderID, err)
			return
		}
	}
	defer notify.Stop(c)

	go w.accumulateChanges(debounceTimeout, dirVsFiles)
	w.log.Infof("Watching %s", w.folderPath)
	// will we ever get out of this loop?
	for {
		evAbsolutePath := w.waitForEvent(c)
		w.log.Debugf("change detected in: %s (could still be ignored)", evAbsolutePath)
		evRelPath := relativePath(evAbsolutePath, w.folderPath)
		if ignores.Match(evRelPath).IsIgnored() {
			w.log.Debugf("ignoring %s", evAbsolutePath)
			continue
		}
		w.fsInput <- evRelPath
	}
}

func relativePath(path string, folderPath string) string {
	path = expandTilde(path)
	path = strings.TrimPrefix(path, folderPath)
	if len(path) == 0 {
		return path
	}
	if os.IsPathSeparator(path[0]) {
		path = path[1:len(path)]
	}
	return path
}

// waitForEvent waits for an event in a channel c and returns event.Path().
// When channel c is closed then it returns path for default event (not sure if this is used at all?)
func (w *Watcher) waitForEvent(c chan notify.EventInfo) string {
	select {
	case ev, ok := <-c:
		if !ok {
			// this is never reached b/c c is never closed
			w.log.Warning("Error: channel closed")
		}
		return ev.Path()
	}
}

// informChange sends a request to rescan folder and subs to Syncthing
func (w *Watcher) informChange(subs []string) error {
	w.log.Debugf("informing ST: %v", subs)
	if err := w.syncClient.Scan(w.folderID, subs, w.delayScan); err != nil {
		w.log.Debugf("failed to perform scan request: %#v", err)
		return maskAny(err)
	}
	return nil
}

func (w *Watcher) askToDelayScan() {
	w.log.Debug("asking to delay full scanning")
	if err := w.informChange([]string{".stfolder"}); err != nil {
		w.log.Warningf("Request to delay scanning failed: %#v", err)
	}
}

// accumulateChanges filters out events that originate from ST.
// - it aggregates changes based on hierarchy structure
// - no redundant folder searches (abc + abc/d is useless)
// - no excessive large scans (abc/{1..1000} should become a scan of just abc folder)
// One of the difficulties is that we cannot know if deleted files were a directory or a file.
func (w *Watcher) accumulateChanges(debounceTimeout time.Duration, dirVsFiles int) {
	var delayScanInterval time.Duration
	if w.delayScan > 0 {
		delayScanInterval = w.delayScan - (5 * time.Second)
		w.log.Debugf("delay scan reminder interval set to %.0f seconds", delayScanInterval.Seconds())
	} else {
		// If delayScan is set to 0, then we never send requests to delay full scans.
		// "9999 * time.Hour" here is an approximation of "forever".
		delayScanInterval = 9999 * time.Hour
		w.log.Debug("delay scan reminders are disabled")
	}
	inProgress := make(map[string]progressTime) // [path string]{fs, start}
	currInterval := delayScanInterval           // Timeout of the timer
	if w.delayScan > 0 {
		w.askToDelayScan()
	}
	nextScanTime := time.Now().Add(delayScanInterval) // Time to remind Syncthing to delay scan
	flushTimer := time.NewTimer(0)
	flushTimerNeedsReset := true
	for {
		if flushTimerNeedsReset {
			flushTimerNeedsReset = false
			flushTimer.Reset(currInterval)
		}
		select {
		case item := <-w.stInput:
			if item.Path == "" {
				// Prepare for incoming changes
				if currInterval != debounceTimeout {
					currInterval = debounceTimeout
					flushTimerNeedsReset = true
				}
				w.log.Debugf("[ST] incoming changes, speeding up inotify timeout parameters")
				continue
			}
			if item.Finished {
				// Ensure path is cleared when receiving itemFinished
				delete(inProgress, item.Path)
				w.log.Debugf("[ST] Removed tracking for %s", item.Path)
				continue
			}
			if len(inProgress) > maxFiles {
				w.log.Debugf("[ST] tracking too many files, aggregating STEvent: %s", item.Path)
				continue
			}
			w.log.Debugf("[ST] incoming: %s", item.Path)
			inProgress[item.Path] = progressTime{false, time.Now()}
		case item := <-w.fsInput:
			if currInterval != debounceTimeout {
				currInterval = debounceTimeout
				flushTimerNeedsReset = true
			}
			w.log.Debugf("[FS] incoming changes, speeding up inotify timeout parameters")
			p, ok := inProgress[item]
			if ok && !p.fsEvent {
				// Change originated from ST
				delete(inProgress, item)
				w.log.Debugf("[FS] Removed tracking for %s", item)
				continue
			}
			if len(inProgress) > maxFiles {
				w.log.Debugf("[FS] tracking too many files, aggregating FSEvent: %s", item)
				continue
			}
			w.log.Debugf("[FS] tracking: %s", item)
			inProgress[item] = progressTime{true, time.Now()}
		case <-flushTimer.C:
			flushTimerNeedsReset = true
			if w.delayScan > 0 && nextScanTime.Before(time.Now()) {
				nextScanTime = time.Now().Add(delayScanInterval)
				w.askToDelayScan()
			}
			if len(inProgress) == 0 {
				if currInterval != delayScanInterval {
					w.log.Debugf("slowing down inotify timeout parameters")
					currInterval = delayScanInterval
				}
				continue
			}
			w.log.Debug("Timeout AccumulateChanges")
			var err error
			var paths []string
			expiry := time.Now().Add(-debounceTimeout * 10)
			if len(inProgress) < maxFiles {
				for path, progress := range inProgress {
					// Clean up invalid and expired paths
					if path == "" || (!progress.fsEvent && progress.time.Before(expiry)) {
						delete(inProgress, path)
						continue
					}
					if progress.fsEvent && time.Now().Sub(progress.time) > currInterval {
						paths = append(paths, path)
						w.log.Debugf("informing about %s", path)
					} else {
						w.log.Debugf("waiting for %s", path)
					}
				}
				if len(paths) == 0 {
					w.log.Debug("empty paths")
					continue
				}

				// Try to inform changes to syncthing and if succeeded, clean up
				err = w.informChange(w.aggregateChanges(dirVsFiles, paths, currentPathStatus))
				if err == nil {
					for _, path := range paths {
						delete(inProgress, path)
						w.log.Debugf("[INFORMED] removed tracking for %s", path)
					}
				}
			} else {
				// Do not track more than maxFiles changes, inform syncthing to rescan entire folder
				err = w.informChange([]string{""})
				if err == nil {
					for path, progress := range inProgress {
						if progress.fsEvent {
							delete(inProgress, path)
							w.log.Debugf("[INFORMED] removed tracking for %s", path)
						}
					}
				}
			}

			if err == nil {
				nextScanTime = time.Now().Add(delayScanInterval) // Scan was delayed
			} else {
				w.log.Warningf("Syncthing failed to index changes for %s: %#v", w.folderID, err)
				time.Sleep(configSyncTimeout)
			}
		}
	}
}

func cleanPaths(paths []string) {
	for i := range paths {
		paths[i] = filepath.Clean(paths[i])
	}
}

func sortedUniqueAndCleanPaths(paths []string) []string {
	cleanPaths(paths)
	sort.Strings(paths)
	var new_paths []string
	previousPath := ""
	for _, path := range paths {
		if path == "." {
			path = ""
		}
		if path != previousPath {
			new_paths = append(new_paths, path)
		}
		previousPath = path
	}
	return new_paths
}

type PathStatus int

const (
	deletedPath PathStatus = iota
	directoryPath
	filePath
)

func currentPathStatus(path string) PathStatus {
	fileinfo, _ := os.Stat(path)
	if fileinfo == nil {
		return deletedPath
	} else if fileinfo.IsDir() {
		return directoryPath
	}
	return filePath
}

type statPathFunc func(name string) PathStatus

// AggregateChanges optimises tracking in two ways:
// - If there are more than `dirVsFiles` changes in a directory, we inform Syncthing to scan the entire directory
// - Directories with parent directory changes are aggregated. If A/B has 3 changes and A/C has 8, A will have 11 changes and if this is bigger than dirVsFiles we will scan A.
func (w *Watcher) aggregateChanges(dirVsFiles int, paths []string, pathStatus statPathFunc) []string {
	// Map paths to scores; if score == -1 the path is a filename
	trackedPaths := make(map[string]int)
	// Map of directories
	trackedDirs := make(map[string]bool)
	// Make sure parent paths are processed first
	paths = sortedUniqueAndCleanPaths(paths)
	// First we collect all paths and calculate scores for them
	for _, path := range paths {
		pathstatus := pathStatus(path)
		path = strings.TrimPrefix(path, w.folderPath)
		path = strings.TrimPrefix(path, pathSeparator)
		var dir string
		if pathstatus == deletedPath {
			// Definitely inform if the path does not exist anymore
			dir = path
			trackedPaths[path] = dirVsFiles
			w.log.Debugf("[AG] Not found: %s", path)
		} else if pathstatus == directoryPath {
			// Definitely inform if a directory changed
			dir = path
			trackedPaths[path] = dirVsFiles
			trackedDirs[dir] = true
			w.log.Debugf("[AG] Is a dir: %s", dir)
		} else {
			w.log.Debugf("[AG] Is file: %s", path)
			// Files are linked to -1 scores
			// Also increment the parent path with 1
			dir = filepath.Dir(path)
			if dir == "." {
				dir = ""
			}
			trackedPaths[path] = -1
			trackedPaths[dir]++
			trackedDirs[dir] = true
		}
		// Search for existing parent directory relations in the map
		for trackedPath := range trackedPaths {
			if trackedDirs[trackedPath] && strings.HasPrefix(dir, trackedPath+pathSeparator) {
				// Increment score of tracked parent directory for each file
				trackedPaths[trackedPath]++
				w.log.Debugf("[AG] Increment: %s %v %v", trackedPath, trackedPaths, trackedPaths[trackedPath])
			}
		}
	}
	var keys []string
	for k := range trackedPaths {
		keys = append(keys, k)
	}
	sort.Strings(keys) // Sort directories before their own files
	previousPath := ""
	var scans []string
	// Decide if we should inform about particular path based on dirVsFiles
	for i := range keys {
		trackedPath := keys[i]
		trackedPathScore := trackedPaths[trackedPath]
		if strings.HasPrefix(trackedPath, previousPath+pathSeparator) {
			// Already informed parent directory change
			continue
		}
		if trackedPathScore < dirVsFiles && trackedPathScore != -1 {
			// Not enough files for this directory or it is a file
			continue
		}
		previousPath = trackedPath
		w.log.Debugf("[AG] Appending path: %s %s", trackedPath, previousPath)
		scans = append(scans, trackedPath)
		if trackedPath == "" {
			// If we need to scan everything, skip the rest
			break
		}
	}
	return scans
}

// watchSTEvents reads events from Syncthing. For events of type ItemStarted and ItemFinished it puts
// them into aproppriate stChans, where key is a folder from event.
// For ConfigSaved event it spawns goroutine waitForSyncAndExitIfNeeded.
func (w *Watcher) watchSTEvents() {
	lastSeenID := 0
	for {
		if w.closed {
			return
		}
		events, err := w.syncClient.GetEvents(lastSeenID)
		if err != nil {
			// Work-around for Go <1.5 (https://github.com/golang/go/issues/9405)
			if strings.Contains(err.Error(), "use of closed network connection") {
				continue
			}
			if w.closed {
				return
			}

			// Syncthing probably restarted
			w.log.Debugf("resetting STEvents: %v", err)
			lastSeenID = 0
			time.Sleep(configSyncTimeout)
			continue
		}
		if events == nil {
			continue
		}
		for _, event := range events {
			switch event.Type {
			case "RemoteIndexUpdated":
				w.stInput <- STEvent{Path: "", Finished: false}
			case "ItemStarted":
				data := event.Data.(map[string]interface{})
				w.stInput <- STEvent{Path: data["item"].(string), Finished: false}
			case "ItemFinished":
				data := event.Data.(map[string]interface{})
				w.stInput <- STEvent{Path: data["item"].(string), Finished: true}
			}
		}
		lastSeenID = events[len(events)-1].ID
	}
}

func getHomeDir() string {
	var home string
	switch runtime.GOOS {
	case "windows":
		home = filepath.Join(os.Getenv("HomeDrive"), os.Getenv("HomePath"))
		if home == "" {
			home = os.Getenv("UserProfile")
		}
	default:
		home = os.Getenv("HOME")
	}
	return home
}

func expandTilde(p string) string {
	if p == "~" {
		return getHomeDir()
	}
	p = filepath.FromSlash(p)
	if !strings.HasPrefix(p, fmt.Sprintf("~%c", os.PathSeparator)) {
		return p
	}
	return filepath.Join(getHomeDir(), p[2:])
}
