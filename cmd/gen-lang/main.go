package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

func main() {
	src := flag.String("src", "CrystalDiskInfo-master/Language", "CrystalDiskInfo language directory")
	out := flag.String("out", "bin/static/lang.generated.js", "output JS file")
	flag.Parse()

	languages, err := readLanguages(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data, err := json.MarshalIndent(languages, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	content := "export const CDI_LANGUAGES = " + string(data) + ";\n"
	if err := os.WriteFile(*out, []byte(content), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readLanguages(root string) (map[string]map[string]map[string]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := map[string]map[string]map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".lang") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		sections, err := readLang(path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if lang := sections["Language"]["LANGUAGE"]; lang != "" {
			name = lang
		}
		result[name] = sections
	}
	return result, nil
}

func readLang(path string) (map[string]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := decodeUTF16LE(raw)
	keep := map[string]bool{
		"Language":     true,
		"DiskStatus":   true,
		"HealthStatus": true,
	}
	sections := map[string]map[string]string{}
	current := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				continue
			}
			current = line[1:end]
			if strings.HasPrefix(current, "Smart") {
				keep[current] = true
			}
			if keep[current] && sections[current] == nil {
				sections[current] = map[string]string{}
			}
			continue
		}
		if !keep[current] {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" {
			sections[current][key] = value
		}
	}
	for section, values := range sections {
		if len(values) == 0 {
			delete(sections, section)
		}
	}
	return sortedSections(sections), nil
}

func decodeUTF16LE(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		raw = raw[2:]
	}
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	return string(utf16.Decode(u))
}

func sortedSections(in map[string]map[string]string) map[string]map[string]string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]map[string]string, len(in))
	for _, key := range keys {
		out[key] = in[key]
	}
	return out
}
