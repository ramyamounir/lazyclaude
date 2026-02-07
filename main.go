package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func init() {
	tview.Borders.Horizontal = '─'
	tview.Borders.Vertical = '│'
	tview.Borders.TopLeft = '╭'
	tview.Borders.TopRight = '╮'
	tview.Borders.BottomLeft = '╰'
	tview.Borders.BottomRight = '╯'
}

// Category represents a subdirectory in the global store (e.g. agents, skills).
type Category struct {
	Name       string // directory name, e.g. "agents"
	GlobalDir  string // ~/.config/claude/agents
	ProjectDir string // /project/.claude/agents
}

// Item represents a single agent, skill, or other resource.
type Item struct {
	Name       string
	IsDir      bool
	GlobalPath string
}

// App holds all application state.
type App struct {
	app             *tview.Application
	pages           *tview.Pages
	panels          []tview.Primitive
	currentPanelIdx int

	availableList *tview.List
	appliedList   *tview.List
	previewView   *tview.TextView
	statusBar     *tview.TextView
	tabBar        *tview.TextView

	categories     []Category
	activeTabIdx   int
	availableItems []Item
	appliedItems   []Item

	projectRoot string
	globalRoot  string

	helpOpen bool
	treeOpen bool
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	projectRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	a := &App{
		projectRoot: projectRoot,
		globalRoot:  filepath.Join(home, ".config", "claude"),
	}

	if err := a.loadCategories(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading categories: %v\n", err)
		os.Exit(1)
	}

	if len(a.categories) == 0 {
		fmt.Fprintf(os.Stderr, "No categories found in %s\n", a.globalRoot)
		os.Exit(1)
	}

	a.setupUI()
	a.refreshAll()

	if err := a.app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// loadCategories scans the global store for subdirectories.
func (a *App) loadCategories() error {
	entries, err := os.ReadDir(a.globalRoot)
	if err != nil {
		return err
	}

	a.categories = nil
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		a.categories = append(a.categories, Category{
			Name:       entry.Name(),
			GlobalDir:  filepath.Join(a.globalRoot, entry.Name()),
			ProjectDir: filepath.Join(a.projectRoot, ".claude", entry.Name()),
		})
	}

	sort.Slice(a.categories, func(i, j int) bool {
		return a.categories[i].Name < a.categories[j].Name
	})

	return nil
}

// loadItems scans a category and partitions into available and applied.
func (a *App) loadItems() {
	cat := a.categories[a.activeTabIdx]
	a.availableItems = nil
	a.appliedItems = nil

	entries, err := os.ReadDir(cat.GlobalDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item := Item{
			Name:       entry.Name(),
			IsDir:      entry.IsDir(),
			GlobalPath: filepath.Join(cat.GlobalDir, entry.Name()),
		}

		projectPath := filepath.Join(cat.ProjectDir, entry.Name())
		if isAppliedSymlink(projectPath, item.GlobalPath) {
			a.appliedItems = append(a.appliedItems, item)
		} else {
			a.availableItems = append(a.availableItems, item)
		}
	}

	sort.Slice(a.availableItems, func(i, j int) bool {
		return a.availableItems[i].Name < a.availableItems[j].Name
	})
	sort.Slice(a.appliedItems, func(i, j int) bool {
		return a.appliedItems[i].Name < a.appliedItems[j].Name
	})
}

// isAppliedSymlink checks if projectPath is a symlink pointing to globalPath.
func isAppliedSymlink(projectPath, globalPath string) bool {
	info, err := os.Lstat(projectPath)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(projectPath)
	if err != nil {
		return false
	}
	// Resolve relative symlinks
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(projectPath), target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	absGlobal, err := filepath.Abs(globalPath)
	if err != nil {
		return false
	}
	if absTarget != absGlobal {
		return false
	}
	// Validate the target still exists
	if _, err := os.Stat(projectPath); err != nil {
		// Broken symlink — clean it up
		os.Remove(projectPath)
		return false
	}
	return true
}

func (a *App) setupUI() {
	a.app = tview.NewApplication()
	selectionColor := tcell.NewRGBColor(106, 159, 181)

	// Tab bar
	a.tabBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	// Available list
	a.availableList = tview.NewList().
		ShowSecondaryText(false).
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(selectionColor).
		SetSelectedTextColor(tcell.ColorWhite)
	a.availableList.SetBorder(true).
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDefault)

	a.availableList.SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		if a.currentPanelIdx == 0 {
			a.updatePreview()
		}
	})

	// Applied list
	a.appliedList = tview.NewList().
		ShowSecondaryText(false).
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(selectionColor).
		SetSelectedTextColor(tcell.ColorWhite)
	a.appliedList.SetBorder(true).
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDefault)

	a.appliedList.SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		if a.currentPanelIdx == 1 {
			a.updatePreview()
		}
	})

	// Preview
	a.previewView = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(true)
	a.previewView.SetBorder(true).
		SetTitle(" Preview ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDefault)

	// Status bar
	a.statusBar = tview.NewTextView().
		SetTextAlign(tview.AlignLeft)

	// Navigable panels (preview is not navigable)
	a.panels = []tview.Primitive{a.availableList, a.appliedList}

	// Layout
	leftFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.tabBar, 1, 0, false).
		AddItem(a.availableList, 0, 1, true).
		AddItem(a.appliedList, 0, 1, false)

	mainFlex := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(leftFlex, 0, 1, true).
		AddItem(a.previewView, 0, 2, false)

	rootFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(mainFlex, 0, 1, true).
		AddItem(a.statusBar, 1, 0, false)

	a.setupKeybindings()
	a.app.SetFocus(a.panels[0])
	a.updateBorderColors()

	a.pages = tview.NewPages().
		AddPage("main", rootFlex, true, true)
	a.app.SetRoot(a.pages, true)
}

func (a *App) setupKeybindings() {
	a.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Modal priority chain
		if a.treeOpen {
			if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
				a.closeTree()
				return nil
			}
			return event
		}
		if a.helpOpen {
			if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
				a.closeHelp()
				return nil
			}
			return event
		}

		switch event.Key() {
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q':
				a.app.Stop()
				return nil
			case '1':
				a.focusPanel(0)
				return nil
			case '2':
				a.focusPanel(1)
				return nil
			case 'h':
				a.prevPanel()
				return nil
			case 'l':
				a.nextPanel()
				return nil
			case 'j':
				a.cursorDown()
				return nil
			case 'k':
				a.cursorUp()
				return nil
			case 'J':
				row, col := a.previewView.GetScrollOffset()
				a.previewView.ScrollTo(row+1, col)
				return nil
			case 'K':
				row, col := a.previewView.GetScrollOffset()
				if row > 0 {
					a.previewView.ScrollTo(row-1, col)
				}
				return nil
			case '[':
				a.prevTab()
				return nil
			case ']':
				a.nextTab()
				return nil
			case ' ':
				a.toggleSelected()
				return nil
			case 't':
				a.showTree()
				return nil
			case '?':
				a.showHelp()
				return nil
			}
		case tcell.KeyEnter:
			a.toggleSelected()
			return nil
		case tcell.KeyTab:
			a.nextPanel()
			return nil
		case tcell.KeyBacktab:
			a.prevPanel()
			return nil
		case tcell.KeyEsc:
			a.app.Stop()
			return nil
		}
		return event
	})
}

// --- Tab switching ---

func (a *App) nextTab() {
	a.activeTabIdx = (a.activeTabIdx + 1) % len(a.categories)
	a.refreshAll()
}

func (a *App) prevTab() {
	a.activeTabIdx = (a.activeTabIdx - 1 + len(a.categories)) % len(a.categories)
	a.refreshAll()
}

// --- Panel navigation ---

func (a *App) focusPanel(idx int) {
	if idx >= 0 && idx < len(a.panels) {
		a.currentPanelIdx = idx
		a.app.SetFocus(a.panels[idx])
		a.updateBorderColors()
		a.updatePreview()
		a.updateStatusBar()
	}
}

func (a *App) nextPanel() {
	a.focusPanel((a.currentPanelIdx + 1) % len(a.panels))
}

func (a *App) prevPanel() {
	a.focusPanel((a.currentPanelIdx - 1 + len(a.panels)) % len(a.panels))
}

func (a *App) updateBorderColors() {
	selectionColor := tcell.NewRGBColor(106, 159, 181)

	for _, p := range a.panels {
		if box, ok := p.(interface {
			SetBorderColor(tcell.Color) *tview.Box
		}); ok {
			box.SetBorderColor(tcell.ColorDefault)
		}
		if list, ok := p.(*tview.List); ok {
			list.SetSelectedBackgroundColor(tcell.ColorDefault)
		}
	}

	focused := a.panels[a.currentPanelIdx]
	if box, ok := focused.(interface {
		SetBorderColor(tcell.Color) *tview.Box
	}); ok {
		box.SetBorderColor(tcell.ColorGreen)
	}
	if list, ok := focused.(*tview.List); ok {
		list.SetSelectedBackgroundColor(selectionColor)
	}
}

// --- Cursor movement ---

func (a *App) cursorDown() {
	if list, ok := a.panels[a.currentPanelIdx].(*tview.List); ok {
		count := list.GetItemCount()
		current := list.GetCurrentItem()
		if current < count-1 {
			list.SetCurrentItem(current + 1)
		}
		a.updatePreview()
	}
}

func (a *App) cursorUp() {
	if list, ok := a.panels[a.currentPanelIdx].(*tview.List); ok {
		current := list.GetCurrentItem()
		if current > 0 {
			list.SetCurrentItem(current - 1)
		}
		a.updatePreview()
	}
}

// --- Toggle (apply/remove) ---

func (a *App) toggleSelected() {
	switch a.currentPanelIdx {
	case 0: // Available panel → apply
		a.applySelected()
	case 1: // Applied panel → remove
		a.removeSelected()
	}
}

func (a *App) applySelected() {
	idx := a.availableList.GetCurrentItem()
	if idx < 0 || idx >= len(a.availableItems) {
		return
	}

	cat := a.categories[a.activeTabIdx]
	item := a.availableItems[idx]

	if err := os.MkdirAll(cat.ProjectDir, 0755); err != nil {
		a.statusBar.SetText(fmt.Sprintf(" [red]Error:[-] %v", err))
		return
	}

	target := filepath.Join(cat.ProjectDir, item.Name)
	if err := os.Symlink(item.GlobalPath, target); err != nil {
		a.statusBar.SetText(fmt.Sprintf(" [red]Error:[-] %v", err))
		return
	}

	a.refreshAll()
}

func (a *App) removeSelected() {
	idx := a.appliedList.GetCurrentItem()
	if idx < 0 || idx >= len(a.appliedItems) {
		return
	}

	cat := a.categories[a.activeTabIdx]
	item := a.appliedItems[idx]

	target := filepath.Join(cat.ProjectDir, item.Name)
	if err := os.Remove(target); err != nil {
		a.statusBar.SetText(fmt.Sprintf(" [red]Error:[-] %v", err))
		return
	}

	a.refreshAll()
}

// --- Refresh ---

func (a *App) refreshAll() {
	a.loadItems()
	a.refreshAvailableList()
	a.refreshAppliedList()
	a.updateTabBar()
	a.updatePanelTitles()
	a.updatePreview()
	a.updateStatusBar()
	a.updateBorderColors()
}

func (a *App) refreshAvailableList() {
	currentIdx := a.availableList.GetCurrentItem()
	a.availableList.Clear()

	for _, item := range a.availableItems {
		prefix := "  "
		if item.IsDir {
			prefix = "[cyan]d[-] "
		}
		a.availableList.AddItem(prefix+item.Name, "", 0, nil)
	}

	if currentIdx >= len(a.availableItems) {
		currentIdx = len(a.availableItems) - 1
	}
	if currentIdx >= 0 {
		a.availableList.SetCurrentItem(currentIdx)
	}
}

func (a *App) refreshAppliedList() {
	currentIdx := a.appliedList.GetCurrentItem()
	a.appliedList.Clear()

	for _, item := range a.appliedItems {
		prefix := "[green]+[-] "
		if item.IsDir {
			prefix = "[green]+[-][cyan]d[-] "
		}
		a.appliedList.AddItem(prefix+item.Name, "", 0, nil)
	}

	if currentIdx >= len(a.appliedItems) {
		currentIdx = len(a.appliedItems) - 1
	}
	if currentIdx >= 0 {
		a.appliedList.SetCurrentItem(currentIdx)
	}
}

func (a *App) updateTabBar() {
	var parts []string
	for i, cat := range a.categories {
		name := strings.Title(cat.Name)
		if i == a.activeTabIdx {
			parts = append(parts, fmt.Sprintf("[green::b] %s [-:-:-]", name))
		} else {
			parts = append(parts, fmt.Sprintf("[darkgray] %s [-]", name))
		}
	}
	a.tabBar.SetText(strings.Join(parts, "│"))
}

func (a *App) updatePanelTitles() {
	catName := strings.Title(a.categories[a.activeTabIdx].Name)
	a.availableList.SetTitle(fmt.Sprintf(" [1] Available %s ", catName))
	a.appliedList.SetTitle(fmt.Sprintf(" [2] Applied %s ", catName))
}

func (a *App) updateStatusBar() {
	a.statusBar.SetText(" [1-2] panels  [j/k] navigate  [J/K] scroll preview  [space/enter] toggle  [/] tabs  [t] tree  [?] help  [q] quit")
}

// --- Preview ---

func (a *App) updatePreview() {
	a.previewView.Clear()

	var item *Item
	switch a.currentPanelIdx {
	case 0:
		idx := a.availableList.GetCurrentItem()
		if idx >= 0 && idx < len(a.availableItems) {
			item = &a.availableItems[idx]
		}
	case 1:
		idx := a.appliedList.GetCurrentItem()
		if idx >= 0 && idx < len(a.appliedItems) {
			item = &a.appliedItems[idx]
		}
	default:
		// Preview panel focused — show whatever was last shown
		return
	}

	if item == nil {
		a.previewView.SetText("[darkgray]No item selected[-]")
		return
	}

	if item.IsDir {
		a.showDirectoryPreview(item)
	} else {
		a.showFilePreview(item)
	}
}

func (a *App) showFilePreview(item *Item) {
	data, err := os.ReadFile(item.GlobalPath)
	if err != nil {
		a.previewView.SetText(fmt.Sprintf("[red]Error reading file:[-] %v", err))
		return
	}

	content := string(data)
	if len(data) > 100*1024 {
		content = string(data[:100*1024])
		content += "\n\n[darkgray]--- truncated (>100KB) ---[-]"
	}

	lang := detectLanguage(item.Name)
	highlighted := highlightCode(content, lang)
	a.previewView.SetText(fmt.Sprintf("[cyan::b]%s[-:-:-]\n\n%s", item.Name, highlighted))
}

func (a *App) showDirectoryPreview(item *Item) {
	// Check for SKILL.md
	skillPath := filepath.Join(item.GlobalPath, "SKILL.md")
	if data, err := os.ReadFile(skillPath); err == nil {
		content := string(data)
		if len(data) > 100*1024 {
			content = string(data[:100*1024])
			content += "\n\n[darkgray]--- truncated (>100KB) ---[-]"
		}
		highlighted := highlightCode(content, "markdown")
		a.previewView.SetText(fmt.Sprintf("[cyan::b]%s/[-:-:-] [darkgray](SKILL.md)[-]\n\n%s", item.Name, highlighted))
		return
	}

	// Fallback: directory listing
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[cyan::b]%s/[-:-:-]\n\n", item.Name))
	a.buildTree(&b, item.GlobalPath, "", 0)
	a.previewView.SetText(b.String())
}

func (a *App) buildTree(b *strings.Builder, dir, prefix string, depth int) {
	if depth > 3 {
		b.WriteString(prefix + "[darkgray]...[-]\n")
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for i, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		isLast := i == len(entries)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		if entry.IsDir() {
			b.WriteString(fmt.Sprintf("%s%s[cyan]%s/[-]\n", prefix, connector, entry.Name()))
			a.buildTree(b, filepath.Join(dir, entry.Name()), childPrefix, depth+1)
		} else {
			b.WriteString(fmt.Sprintf("%s%s%s\n", prefix, connector, entry.Name()))
		}
	}
}

// --- Tree modal ---

func (a *App) showTree() {
	// Get the currently selected item
	var item *Item
	switch a.currentPanelIdx {
	case 0:
		idx := a.availableList.GetCurrentItem()
		if idx >= 0 && idx < len(a.availableItems) {
			item = &a.availableItems[idx]
		}
	case 1:
		idx := a.appliedList.GetCurrentItem()
		if idx >= 0 && idx < len(a.appliedItems) {
			item = &a.appliedItems[idx]
		}
	}

	if item == nil || !item.IsDir {
		return
	}

	a.treeOpen = true

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[cyan::b]%s/[-:-:-]\n\n", item.Name))
	a.buildTree(&b, item.GlobalPath, "", 0)
	b.WriteString("\n[darkgray]Press Escape or q to close[-]")

	treeText := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetText(b.String())
	treeText.SetBorder(true).
		SetTitle(fmt.Sprintf(" %s — Tree ", item.Name)).
		SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorGreen)

	a.pages.AddPage("tree", modal(treeText, 60, 25), true, true)
	a.app.SetFocus(treeText)
}

func (a *App) closeTree() {
	a.treeOpen = false
	a.pages.RemovePage("tree")
	a.app.SetFocus(a.panels[a.currentPanelIdx])
	a.updateBorderColors()
}

// --- Help modal ---

func (a *App) showHelp() {
	a.helpOpen = true

	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetText(`[yellow::b]LazyClaude — Help[-:-:-]

[green]Navigation:[-]
  1, 2          Jump to panel
  Tab / S-Tab   Cycle panels
  h / l         Prev / Next panel
  j / k         Move cursor
  J / K         Scroll preview

[green]Tabs:[-]
  [ / ]         Prev / Next category

[green]Actions:[-]
  Space / Enter Apply or remove item
                (Available → apply, Applied → remove)
  t             Show folder tree (directories)

[green]Meta:[-]
  q / Esc       Quit
  ?             This help

[darkgray]Press Escape or q to close[-]`)

	helpText.SetBorder(true).
		SetTitle(" Help ").
		SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorGreen)

	a.pages.AddPage("help", modal(helpText, 55, 22), true, true)
	a.app.SetFocus(helpText)
}

func (a *App) closeHelp() {
	a.helpOpen = false
	a.pages.RemovePage("help")
	a.app.SetFocus(a.panels[a.currentPanelIdx])
	a.updateBorderColors()
}

func modal(content tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(content, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)
}

// --- Syntax highlighting ---

func highlightCode(code, language string) string {
	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("gruvbox")
	if style == nil {
		style = styles.Fallback
	}

	var buf strings.Builder
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return tview.Escape(code)
	}

	for token := iterator(); token != chroma.EOF; token = iterator() {
		entry := style.Get(token.Type)
		text := tview.Escape(token.Value)
		if entry.Colour.IsSet() {
			r, g, b := entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue()
			buf.WriteString(fmt.Sprintf("[#%02x%02x%02x]%s[-]", r, g, b, text))
		} else {
			buf.WriteString(text)
		}
	}
	return buf.String()
}

func detectLanguage(filename string) string {
	ext := filepath.Ext(filename)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".sh", ".bash":
		return "bash"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".toml":
		return "toml"
	default:
		return ""
	}
}
