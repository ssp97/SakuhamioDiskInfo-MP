package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// IniTree represents a parsed INI file as a tree:
// section -> key -> value (raw string without semicolons)
type IniTree map[string]map[string]string

type ThemeData struct {
	Themes      map[string]ThemeOutput
	Default     string
	DefaultDark string
}

// ThemeOutput is the enhanced theme entry for themes.json
type ThemeOutput struct {
	Name         string            `json:"name"`
	ParentTheme1 string            `json:"parentTheme1,omitempty"`
	ParentTheme2 string            `json:"parentTheme2,omitempty"`
	Author       string            `json:"author,omitempty"`
	Position     *int              `json:"position,omitempty"`
	GlassAlpha   int               `json:"glassAlpha,omitempty"`
	Colors       map[string]string `json:"colors"`
	Images       map[string]string `json:"images"`
}

func main() {
	themesDir := "CrystalDiskInfo/CdiResource/themes"
	outDir := "bin/static/themes"
	if len(os.Args) > 1 {
		themesDir = os.Args[1]
	}
	if len(os.Args) > 2 {
		outDir = os.Args[2]
	}
	if samePath(themesDir, outDir) {
		fmt.Fprintf(os.Stderr, "Source and output theme dirs must be different: %s\n", themesDir)
		os.Exit(1)
	}

	outFile := filepath.Join(outDir, "themes.json")
	if err := resetOutputDir(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error preparing output dir: %v\n", err)
		os.Exit(1)
	}

	// 1. Discover theme directories
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading themes dir: %v\n", err)
		os.Exit(1)
	}

	var themeDirs []string
	for _, e := range entries {
		if e.IsDir() {
			themeDirs = append(themeDirs, e.Name())
		}
	}
	output := ThemeData{
		Themes:      make(map[string]ThemeOutput),
		Default:     "Shizuku",
		DefaultDark: "ShizukuDark~nijihashi_sola",
	}
	for _, name := range themeDirs {
		iniPath := filepath.Join(themesDir, name, "theme.ini")
		tree, err := parseIni(iniPath)
		if err != nil {
			// Silently skip themes without valid INI
			continue
		}
		var out ThemeOutput
		out.Name = name
		if info, ok := tree["Info"]; ok {
			out.Author = info["Author"]
			out.ParentTheme1 = info["ParentTheme1"]
			out.ParentTheme2 = info["ParentTheme2"]
		}

		// Character section
		if char, ok := tree["Character"]; ok {
			if pos, err := strconv.Atoi(char["Position"]); err == nil {
				p := pos
				out.Position = &p
			}
		}

		// Alpha section
		if alpha, ok := tree["Alpha"]; ok {
			if ga, err := strconv.Atoi(alpha["GlassAlpha"]); err == nil {
				out.GlassAlpha = ga
			}
		}

		if color, ok := tree["Color"]; ok {
			out.Colors = color
			for key, value := range out.Colors {
				out.Colors[key] = colorToCSS(value) //fmt.Sprintf("#%06s", value[2:len(value)-1])
			}
		}
		themePath := filepath.Join(themesDir, name)
		list, err := os.ReadDir(themePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading theme dir: %v\n", err)
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Name() > list[j].Name()
		})
		var lastPrefix string
		out.Images = make(map[string]string, len(list))
		for _, fi := range list {
			if fi.IsDir() || !isImage(fi.Name()) {
				continue
			}
			name := fi.Name()
			i := strings.LastIndex(name, "-")
			if i == -1 {
				continue
			}
			pref := name[:i]
			if pref != lastPrefix {
				webpName := strings.TrimSuffix(fi.Name(), filepath.Ext(fi.Name())) + ".webp"
				if err := convertWebP(filepath.Join(themePath, fi.Name()), filepath.Join(outDir, out.Name, webpName)); err != nil {
					fmt.Fprintf(os.Stderr, "Error converting %s: %v\n", filepath.Join(themePath, fi.Name()), err)
					os.Exit(1)
				}
				out.Images[pref] = webpName
				lastPrefix = pref
			}
		}

		output.Themes[name] = out
	}

	// 2. Parse all theme.ini files into IniTree map
	//parsed := make(map[string]IniTree)
	//for _, name := range themeDirs {
	//	iniPath := filepath.Join(themesDir, name, "theme.ini")
	//	tree, err := parseIni(iniPath)
	//	if err != nil {
	//		// Silently skip themes without valid INI
	//		continue
	//	}
	//	parsed[name] = tree
	//}
	//
	//// 3. Build outputs with parent resolution
	//var outputs []ThemeOutput
	//for _, name := range themeDirs {
	//	tree, ok := parsed[name]
	//	if !ok {
	//		continue
	//	}
	//
	//	var out ThemeOutput
	//	out.Name = name
	//
	//	// Info section
	//	if info, ok := tree["Info"]; ok {
	//		out.Author = info["Author"]
	//		out.ParentTheme1 = info["ParentTheme1"]
	//		out.ParentTheme2 = info["ParentTheme2"]
	//	}
	//
	//	// Character section
	//	if char, ok := tree["Character"]; ok {
	//		if pos, err := strconv.Atoi(char["Position"]); err == nil {
	//			p := pos
	//			out.Position = &p
	//		}
	//	}
	//
	//	// Alpha section
	//	if alpha, ok := tree["Alpha"]; ok {
	//		if ga, err := strconv.Atoi(alpha["GlassAlpha"]); err == nil {
	//			out.GlassAlpha = ga
	//		}
	//	}
	//
	//	// Resolve colors and images with parent chain: self -> ParentTheme1 -> ParentTheme2
	//	out.Colors = resolveColors(name, tree, parsed)
	//	fixContrast(&out.Colors)
	//	out.Images = resolveImages(name, tree, parsed, themesDir)
	//
	//	outputs = append(outputs, out)
	//}

	// 4. Write themes.json
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outFile, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing themes.json: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s with %d themes from %s\n", outFile, len(output.Themes), themesDir)
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
}

func resetOutputDir(dir string) error {
	clean := filepath.Clean(dir)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("refusing to reset unsafe output dir: %s", dir)
	}
	if err := os.RemoveAll(clean); err != nil {
		return err
	}
	return os.MkdirAll(clean, 0755)
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff":
		return true
	default:
		return false
	}
}

func convertWebP(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	// cwebp has no "anime" preset; drawing is the built-in preset closest to anime/line art.
	args := []string{"-preset", "drawing", "-lossless", "-m", "6", "-mt", src, "-o", dst}
	out, err := exec.Command("cwebp", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseIni reads an INI file and returns a tree: section -> key -> value
func parseIni(path string) (IniTree, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	tree := make(IniTree)
	currentSection := ""

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end > 1 {
				currentSection = line[1:end]
				if tree[currentSection] == nil {
					tree[currentSection] = make(map[string]string)
				}
			}
			continue
		}

		// Key=Value (strip inline semicolon comment)
		if currentSection != "" {
			eq := strings.Index(line, "=")
			if eq > 0 {
				key := strings.TrimSpace(line[:eq])
				val := strings.TrimSpace(line[eq+1:])
				// Strip trailing semicolon comments
				if idx := strings.Index(val, ";"); idx >= 0 {
					val = strings.TrimSpace(val[:idx])
				}
				tree[currentSection][key] = val
			}
		}
	}

	return tree, nil
}

// Colors maps color key names to #RRGGBB CSS values
type Colors struct {
	LabelText        string `json:"labelText,omitempty"`
	ButtonText       string `json:"buttonText,omitempty"`
	ListText1        string `json:"listText1,omitempty"`
	ListText2        string `json:"listText2,omitempty"`
	ListTextSelected string `json:"listTextSelected,omitempty"`
	ListBk1          string `json:"listBk1,omitempty"`
	ListBk2          string `json:"listBk2,omitempty"`
	ListBkSelected   string `json:"listBkSelected,omitempty"`
	ListLine1        string `json:"listLine1,omitempty"`
	ListLine2        string `json:"listLine2,omitempty"`
	Glass            string `json:"glass,omitempty"`
	Frame            string `json:"frame,omitempty"`
}

type Images struct {
	Background      string `json:"background,omitempty"`
	DiskGood        string `json:"diskGood,omitempty"`
	DiskCaution     string `json:"diskCaution,omitempty"`
	DiskBad         string `json:"diskBad,omitempty"`
	DiskUnknown     string `json:"diskUnknown,omitempty"`
	TemperatureGood string `json:"temperatureGood,omitempty"`
	TemperatureBad  string `json:"temperatureBad,omitempty"`
	SDGood          string `json:"sdGood,omitempty"`
	SDCaution       string `json:"sdCaution,omitempty"`
	SDBad           string `json:"sdBad,omitempty"`
	SDUnknown       string `json:"sdUnknown,omitempty"`
	PreDisk         string `json:"preDisk,omitempty"`
	NextDisk        string `json:"nextDisk,omitempty"`
	Copyright       string `json:"copyright,omitempty"`
}

// colorToCSS converts "0xRRGGBB" to "#RRGGBB" CSS format
func colorToCSS(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		hex := raw[2:]
		// Pad to 6 chars
		for len(hex) < 6 {
			hex = "0" + hex
		}
		// Validate it's valid hex
		for _, c := range hex {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return ""
			}
		}
		return "#" + strings.ToUpper(hex)
	}
	return ""
}

// resolveColors resolves the color chain with parent inheritance
func resolveColors(name string, tree IniTree, parsed map[string]IniTree) Colors {
	// Build resolution chain: self -> ParentTheme1 -> ParentTheme2
	var chain []IniTree
	chain = append(chain, tree)

	if info, ok := tree["Info"]; ok {
		if p1 := info["ParentTheme1"]; p1 != "" {
			if t, ok := parsed[p1]; ok {
				chain = append(chain, t)
			}
		}
		if p2 := info["ParentTheme2"]; p2 != "" {
			if t, ok := parsed[p2]; ok {
				chain = append(chain, t)
			}
		}
	}

	// Resolve each color key from the chain
	keys := []string{"LabelText", "ButtonText", "ListText1", "ListText2", "ListTextSelected", "ListBk1", "ListBk2", "ListBkSelected", "ListLine1", "ListLine2", "Glass", "Frame"}
	resolved := make(map[string]string)
	for _, key := range keys {
		for _, t := range chain {
			if colors, ok := t["Color"]; ok {
				if val, ok := colors[key]; ok && val != "" {
					if css := colorToCSS(val); css != "" {
						resolved[key] = css
						break
					}
				}
			}
		}
	}

	return Colors{
		LabelText:        resolved["LabelText"],
		ButtonText:       resolved["ButtonText"],
		ListText1:        resolved["ListText1"],
		ListText2:        resolved["ListText2"],
		ListTextSelected: resolved["ListTextSelected"],
		ListBk1:          resolved["ListBk1"],
		ListBk2:          resolved["ListBk2"],
		ListBkSelected:   resolved["ListBkSelected"],
		ListLine1:        resolved["ListLine1"],
		ListLine2:        resolved["ListLine2"],
		Glass:            resolved["Glass"],
		Frame:            resolved["Frame"],
	}
}

func fixContrast(c *Colors) {
	text := c.ButtonText
	if text == "" {
		text = c.LabelText
	}
	bg := c.Glass
	if bg == "" {
		bg = c.ListBk1
	}
	if contrast(text, bg) < 3 && contrast(c.LabelText, bg) >= 3 {
		c.ButtonText = c.LabelText
	}
	if c.ListTextSelected == "" {
		c.ListTextSelected = c.ListText1
	}
	if c.ListBkSelected == "" {
		c.ListBkSelected = c.ListBk2
	}
}

func contrast(a, b string) float64 {
	la, oka := luminance(a)
	lb, okb := luminance(b)
	if !oka || !okb {
		return 21
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(css string) (float64, bool) {
	if len(css) != 7 || css[0] != '#' {
		return 0, false
	}
	r, err1 := strconv.ParseUint(css[1:3], 16, 8)
	g, err2 := strconv.ParseUint(css[3:5], 16, 8)
	b, err3 := strconv.ParseUint(css[5:7], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return channelLum(float64(r)/255)*0.2126 + channelLum(float64(g)/255)*0.7152 + channelLum(float64(b)/255)*0.0722, true
}

func channelLum(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func resolveImages(name string, tree IniTree, parsed map[string]IniTree, themesDir string) Images {
	type item struct {
		name string
		tree IniTree
	}
	chain := []item{{name: name, tree: tree}}
	if info, ok := tree["Info"]; ok {
		for _, key := range []string{"ParentTheme1", "ParentTheme2"} {
			parent := info[key]
			if parent != "" {
				if t, ok := parsed[parent]; ok {
					chain = append(chain, item{name: parent, tree: t})
				}
			}
		}
	}

	find := func(baseNames ...string) string {
		for _, it := range chain {
			for _, base := range baseNames {
				if file := findImage(filepath.Join(themesDir, it.name), base); file != "" {
					return it.name + "/" + file
				}
			}
		}
		return ""
	}

	images := Images{
		DiskGood:        find("diskStatusGood", "diskGood"),
		DiskCaution:     find("diskStatusCaution", "diskCaution"),
		DiskBad:         find("diskStatusBad", "diskBad"),
		DiskUnknown:     find("diskStatusUnknown", "diskUnknown"),
		TemperatureGood: find("temperatureGood"),
		TemperatureBad:  find("temperatureBad"),
		SDGood:          find("SDdiskStatusGood100", "SDdiskStatusGood"),
		SDCaution:       find("SDdiskStatusCaution"),
		SDBad:           find("SDdiskStatusBad"),
		SDUnknown:       find("SDdiskStatusUnknown"),
		PreDisk:         find("preDisk"),
		NextDisk:        find("nextDisk"),
		Copyright:       find(name+"Copyright", "ShizukuCopyright", "Copyright"),
	}

	if colors, ok := tree["Color"]; ok {
		if bg, ok := colors["Background"]; ok {
			if strings.EqualFold(bg, "0xFFFFFFFF") {
				return images
			}
		}
	}
	images.Background = find(name+"Background", strings.ReplaceAll(name, "~", "_")+"Background", "ShizukuBackground", "Background")
	return images
}

func findImage(dirPath, base string) string {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return ""
	}
	dpiLevels := []string{"300", "250", "200", "150", "125", "100"}
	for _, dpi := range dpiLevels {
		pattern := base + "-" + dpi + ".png"
		for _, e := range entries {
			if e.Name() == pattern {
				return pattern
			}
		}
	}

	return ""
}
