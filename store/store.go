package store

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/udhos/equalfile"
)

type hasPrintf interface {
	Printf(fmt string, v ...interface{})
}

type sortByCommitId struct {
	data   []string
	logger hasPrintf
}

func (s sortByCommitId) Len() int {
	return len(s.data)
}
func (s sortByCommitId) Swap(i, j int) {
	s.data[i], s.data[j] = s.data[j], s.data[i]
}
func (s sortByCommitId) Less(i, j int) bool {
	s1 := s.data[i]
	id1, err1 := ExtractCommitIdFromFilename(s1)
	if err1 != nil {
		s.logger.Printf("sortByCommitId.Less: error parsing config file path: '%s': %v", s1, err1)
	}
	s2 := s.data[j]
	id2, err2 := ExtractCommitIdFromFilename(s2)
	if err2 != nil {
		s.logger.Printf("sortByCommitId.Less: error parsing config file path: '%s': %v", s2, err2)
	}
	return id1 < id2
}

func ExtractCommitIdFromFilename(filename string) (int, error) {
	lastDot := strings.LastIndexByte(filename, '.')
	commitId := filename[lastDot+1:]
	id, err := strconv.Atoi(commitId)
	if err != nil {
		return -1, fmt.Errorf("extractCommitIdFromFilename: error parsing filename [%s]: %v", filename, err)
	}

	return id, nil
}

func FindLastConfig(configPathPrefix string, logger hasPrintf) (string, error) {

	lastIdPath := getLastIdPath(configPathPrefix)
	f, openErr := os.Open(lastIdPath)
	if openErr == nil {
		defer f.Close()
		r := bufio.NewReader(f)
		line, _, readErr := r.ReadLine()
		if readErr == nil {
			id := string(line[:])
			path := getConfigPath(configPathPrefix, id)
			_, statErr := os.Stat(path)
			if statErr == nil {
				logger.Printf("FindLastConfig: found from shortcut: '%s'", path)
				return path, nil // found
			}

			logger.Printf("FindLastConfig: stat failure '%s': %v", lastIdPath, statErr)

		} else {
			logger.Printf("FindLastConfig: read failure '%s': %v", lastIdPath, readErr)
		}
	}
	logger.Printf("FindLastConfig: last id file not found '%s': %v", lastIdPath, openErr)

	// search filesystem directory

	dirname, matches, err := ListConfig(configPathPrefix, logger)
	if err != nil {
		return "", err
	}

	size := len(matches)

	logger.Printf("FindLastConfig: found %d matching files: %v", size, matches)

	if size < 1 {
		return "", fmt.Errorf("FindLastConfig: no config file found for prefix: %s", configPathPrefix)
	}

	maxId := -1
	last := ""
	for _, m := range matches {
		id, idErr := ExtractCommitIdFromFilename(m)
		if idErr != nil {
			return "", fmt.Errorf("FindLastConfig: bad commit id: %s: %v", m, idErr)
		}
		if id >= maxId {
			maxId = id
			last = m
		}
	}

	lastPath := filepath.Join(dirname, last)

	logger.Printf("FindLastConfig: found: %s", lastPath)

	return lastPath, nil
}

func ListConfigSorted(configPathPrefix string, reverse bool, logger hasPrintf) (string, []string, error) {

	dirname, matches, err := ListConfig(configPathPrefix, logger)
	if err != nil {
		return dirname, matches, err
	}

	if reverse {
		sort.Sort(sort.Reverse(sortByCommitId{data: matches, logger: logger}))
	} else {
		sort.Sort(sortByCommitId{data: matches, logger: logger})
	}

	return dirname, matches, nil
}

func ListConfig(configPathPrefix string, logger hasPrintf) (string, []string, error) {

	dirname := filepath.Dir(configPathPrefix)

	dir, err := os.Open(dirname)
	if err != nil {
		return "", nil, fmt.Errorf("ListConfig: error opening dir '%s': %v", dirname, err)
	}

	names, e := dir.Readdirnames(0)
	if e != nil {
		return "", nil, fmt.Errorf("ListConfig: error reading dir '%s': %v", dirname, e)
	}

	dir.Close()

	logger.Printf("ListConfig: prefix=[%s] names=%d", configPathPrefix, len(names))

	basename := filepath.Base(configPathPrefix)

	// filter prefix
	matches := names[:0] // slice trick: Filtering without allocating
	for _, x := range names {
		lastByte := rune(x[len(x)-1])
		if unicode.IsDigit(lastByte) && strings.HasPrefix(x, basename) {
			matches = append(matches, x)
		}
	}

	return dirname, matches, nil
}

type HasWrite interface {
	Write(p []byte) (int, error)
}

func getLastIdPath(configPathPrefix string) string {
	return fmt.Sprintf("%slast", configPathPrefix)
}

func getConfigPath(configPathPrefix, id string) string {
	return fmt.Sprintf("%s%s", configPathPrefix, id)
}

func SaveNewConfig(configPathPrefix string, maxFiles int, logger hasPrintf, writeFunc func(HasWrite) error, changesOnly bool) (string, error) {

	// get tmp file

	tmpPath := getConfigPath(configPathPrefix, "tmp")

	if _, err := os.Stat(tmpPath); err == nil {
		return "", fmt.Errorf("SaveNewConfig: tmp file exists: [%s]", tmpPath)
	}

	f, createErr := os.Create(tmpPath)
	if createErr != nil {
		return "", fmt.Errorf("SaveNewConfig: error creating tmp file: [%s]: %v", tmpPath, createErr)
	}

	defer os.Remove(tmpPath)

	// write to tmp file

	w := bufio.NewWriter(f)

	if err := writeFunc(w); err != nil {
		return "", fmt.Errorf("SaveNewConfig: writeFunc error: [%s]: %v", tmpPath, err)
	}

	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("SaveNewConfig: error flushing file: [%s]: %v", tmpPath, err)
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("SaveNewConfig: error closing file: [%s]: %v", tmpPath, err)
	}

	// get previous file

	previousFound := true
	lastConfig, err1 := FindLastConfig(configPathPrefix, logger)
	if err1 != nil {
		logger.Printf("SaveNewConfig: error reading config: [%s]: %v", configPathPrefix, err1)
		previousFound = false
	}

	id, err2 := ExtractCommitIdFromFilename(lastConfig)
	if err2 != nil {
		logger.Printf("SaveNewConfig: error parsing config path: [%s]: %v", lastConfig, err2)
	}

	if changesOnly && previousFound {
		equal, equalErr := equalfile.CompareFile(lastConfig, tmpPath)
		if equalErr == nil {
			if equal {
				logger.Printf("SaveNewConfig: refusing to create identical new file: [%s]", tmpPath)
				if removeErr := os.Remove(tmpPath); removeErr != nil {
					logger.Printf("SaveNewConfig: error removing temp file=[%s]: %v", tmpPath, removeErr)
				}
				return lastConfig, nil // success
			}
			// unequal
			logger.Printf("SaveNewConfig: files differ previous=[%s] new=[%s]", lastConfig, tmpPath)
		} else {
			// unable to compare
			logger.Printf("SaveNewConfig: error comparing previous=[%s] to new=[%s]: %v", lastConfig, tmpPath, equalErr)
		}
	}

	// get new file

	newCommitId := id + 1
	newFilepath := getConfigPath(configPathPrefix, strconv.Itoa(newCommitId))

	logger.Printf("SaveNewConfig: newPath=[%s]", newFilepath)

	if _, err := os.Stat(newFilepath); err == nil {
		return "", fmt.Errorf("SaveNewConfig: new file exists: [%s]", newFilepath)
	}

	// rename tmp to new file

	if renameErr := os.Rename(tmpPath, newFilepath); renameErr != nil {
		return "", fmt.Errorf("SaveNewConfig: could not rename '%s' to '%s'; %v", tmpPath, newFilepath, renameErr)
	}

	// write shortcut file

	// write last id into shortcut file
	lastIdPath := getLastIdPath(configPathPrefix)
	if err := ioutil.WriteFile(lastIdPath, []byte(strconv.Itoa(newCommitId)), 0700); err != nil {
		logger.Printf("SaveNewConfig: error writing last id file '%s': %v", lastIdPath, err)

		// since we failed to update the shortcut file,
		// it might be pointing to old backup.
		// then it's safer to simply remove it.
		os.Remove(lastIdPath)
	}

	// erase old file

	eraseOldFiles(configPathPrefix, maxFiles, logger)

	return newFilepath, nil
}

func eraseOldFiles(configPathPrefix string, maxFiles int, logger hasPrintf) {

	if maxFiles < 1 {
		return
	}

	dirname, matches, err := ListConfigSorted(configPathPrefix, false, logger)
	if err != nil {
		logger.Printf("eraseOldFiles: %v", err)
		return
	}

	totalFiles := len(matches)

	toDelete := totalFiles - maxFiles
	if toDelete < 1 {
		logger.Printf("eraseOldFiles: nothing to delete existing=%d <= max=%d", totalFiles, maxFiles)
		return
	}

	for i := 0; i < toDelete; i++ {
		path := filepath.Join(dirname, matches[i])
		logger.Printf("eraseOldFiles: delete: [%s]", path)
		if err := os.Remove(path); err != nil {
			logger.Printf("eraseOldFiles: delete: error: [%s]: %v", path, err)
		}
	}
}
