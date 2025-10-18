// main.go (упрощённая версия: только просмотр, TOC работает для preview)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	// "unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// ---------------- key bindings ----------------

type keyMap struct {
	Up, Down         key.Binding
	Open, Back       key.Binding
	ToggleHidden     key.Binding
	Tab, Help, Close key.Binding
	Quit             key.Binding
	ShowPath         key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:           key.NewBinding(key.WithKeys("up")),
		Down:         key.NewBinding(key.WithKeys("down")),
		Open:         key.NewBinding(key.WithKeys("right")),
		Back:         key.NewBinding(key.WithKeys("left")),
		ToggleHidden: key.NewBinding(key.WithKeys(".")),
		Tab:          key.NewBinding(key.WithKeys("tab")),
		Help:         key.NewBinding(key.WithKeys("?")),
		Close:        key.NewBinding(key.WithKeys("esc")),
		Quit:         key.NewBinding(key.WithKeys("ctrl+q")),
		ShowPath:     key.NewBinding(key.WithKeys("ctrl+p")),
	}
}

// ---------------- model ----------------

type fileItem struct {
	name  string
	path  string
	isDir bool
}

type model struct {
	width     int
	height    int
	leftWidth int
	ready     bool
	lastWrap  int

	cwd        string
	files      []fileItem
	cursor     int
	leftOffset int
	showHidden bool

	vp             viewport.Model
	renderer       *glamour.TermRenderer
	current        string // current file path
	currentContent string // raw file content (used for TOC)
	active         string // "left", "right", "toc"
	showHelp       bool
	keys           keyMap

	showPathPopup bool
	filePath      string

	// ---- TOC ----
	tocVisible  bool
	tocHeadings []Heading
	tocCursor   int
	tocOffset   int
}

// message sent when markdown renderer is built asynchronously
type rendererReadyMsg struct{ r *glamour.TermRenderer }

// async command to build a glamour renderer with given wrap width
func buildRendererCmd(wrap int) tea.Cmd {
	if wrap < 1 {
		wrap = 80
	}
	return func() tea.Msg {
		r, _ := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(wrap),
		)
		return rendererReadyMsg{r: r}
	}
}

// ---- TOC heading ----
type Heading struct {
	Level int
	Title string
	Line  int
}

var headingRe = regexp.MustCompile(`^(#{1,6})\s*(.+?)\s*(?:#+\s*)?$`)

func parseHeadings(content string) []Heading {
	var result []Heading
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			result = append(result, Heading{
				Level: len(m[1]),
				Title: m[2],
				Line:  i,
			})
		}
	}
	return result
}

// ---------------- initialization ----------------

func initialModel() model {
	vp := viewport.New(0, 0)
	// Try to get initial terminal size to avoid undersized first render
	w, h, termOK := 0, 0, false
	if tw, th, err := term.GetSize(int(os.Stdout.Fd())); err == nil && tw > 0 && th > 0 {
		w, h, termOK = tw, th, true
	}
	rightW := 80
	if termOK {
		rightW = w - 36 - 4
		if rightW < 20 {
			rightW = 20
		}
	}
	// Build a minimal renderer now (cheap) — we will rebuild async after size known
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(rightW-1),
	)

	cwd, _ := os.Getwd()
	files := loadFiles(cwd, false)

	m := model{
		width:          100,
		height:         30,
		leftWidth:      36,
		ready:          false,
		cwd:            cwd,
		files:          files,
		cursor:         0,
		leftOffset:     0,
		showHidden:     false,
		vp:             vp,
		renderer:       renderer,
		current:        "",
		currentContent: "",
		active:         "left",
		showHelp:       false,
		keys:           newKeyMap(),
		showPathPopup:  false,
		filePath:       "",
		tocVisible:     false,
		tocHeadings:    nil,
		tocCursor:      0,
		tocOffset:      0,
	}
	if termOK {
		m.width, m.height = w, h
		m.ready = true
		m.vp.Width = rightW - 1
		m.vp.Height = m.height - 2
		m.lastWrap = rightW - 1
	}
	return m
}

// ---------------- helpers ----------------

func loadFiles(dir string, showHidden bool) []fileItem {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []fileItem

	for _, e := range entries {
		if !showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		items = append(items, fileItem{
			name:  e.Name(),
			path:  filepath.Join(dir, e.Name()),
			isDir: e.IsDir(),
		})
	}

	// Sort: directories first, then files; alphabetical by name
	sort.Slice(items, func(i, j int) bool {
		if items[i].isDir != items[j].isDir {
			return items[i].isDir && !items[j].isDir
		}
		return strings.ToLower(items[i].name) < strings.ToLower(items[j].name)
	})
	return items
}

// highlightSyntax применяет подсветку синтаксиса к содержимому файла
func highlightSyntax(content, filename string) (string, error) {
	var lexer chroma.Lexer
	switch {
	case strings.HasSuffix(strings.ToLower(filename), ".go"):
		lexer = lexers.Get("go")
	case strings.HasSuffix(strings.ToLower(filename), ".py"):
		lexer = lexers.Get("python")
	default:
		return content, nil // Для других типов файлов возвращаем содержимое как есть
	}

	if lexer == nil {
		return content, nil
	}

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content, err
	}

	formatter := formatters.Get("terminal")
	if formatter == nil {
		return content, nil
	}

	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}

	var buf strings.Builder
	err = formatter.Format(&buf, style, iterator)
	if err != nil {
		return content, err
	}

	return buf.String(), nil
}

// clampLeftOffset корректирует leftOffset для плавного скролла списка файлов
func (m *model) clampLeftOffset() {
	visible := m.height - 4
	if visible < 1 {
		visible = 1
	}

	if len(m.files) <= visible {
		m.leftOffset = 0
		return
	}

	if m.cursor < m.leftOffset {
		m.leftOffset = m.cursor
	}
	if m.cursor >= m.leftOffset+visible {
		m.leftOffset = m.cursor - visible + 1
	}

	if m.leftOffset < 0 {
		m.leftOffset = 0
	}
	if m.leftOffset > len(m.files)-visible {
		m.leftOffset = len(m.files) - visible
	}
}

// ---------------- update ----------------

func (m model) Init() tea.Cmd {
	if m.ready {
		rightW := m.width - m.leftWidth - 4
		if rightW < 20 {
			rightW = 20
		}
		return buildRendererCmd(rightW - 1)
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	mPtr := &m

	switch msg := msg.(type) {
	case rendererReadyMsg:
		if msg.r != nil {
			mPtr.renderer = msg.r
		}
		return *mPtr, nil
	case tea.WindowSizeMsg:
		mPtr.width, mPtr.height = msg.Width, msg.Height
		rightW := mPtr.width - mPtr.leftWidth - 4
		if rightW < 20 {
			rightW = 20
		}
		mPtr.vp.Width = rightW - 1
		mPtr.vp.Height = mPtr.height - 2
		mPtr.ready = true
		// Rebuild renderer asynchronously only if wrap changed
		if wrap := rightW - 1; wrap != mPtr.lastWrap {
			mPtr.lastWrap = wrap
			cmd = buildRendererCmd(wrap)
		}
		mPtr.clampLeftOffset()
		if mPtr.tocOffset < 0 {
			mPtr.tocOffset = 0
		}
		return *mPtr, cmd

	case tea.KeyMsg:
		if key.Matches(msg, mPtr.keys.Help) {
			mPtr.showHelp = !mPtr.showHelp
			return *mPtr, nil
		}
		if mPtr.showHelp {
			if key.Matches(msg, mPtr.keys.Close) {
				mPtr.showHelp = false
			}
			return *mPtr, nil
		}

		if key.Matches(msg, mPtr.keys.Quit) {
			return *mPtr, tea.Quit
		}

		// Toggle hidden files
		if key.Matches(msg, mPtr.keys.ToggleHidden) {
			mPtr.showHidden = !mPtr.showHidden
			mPtr.files = loadFiles(mPtr.cwd, mPtr.showHidden)
			if mPtr.cursor >= len(mPtr.files) {
				mPtr.cursor = len(mPtr.files) - 1
			}
			if mPtr.cursor < 0 {
				mPtr.cursor = 0
			}
			mPtr.clampLeftOffset()
			return *mPtr, nil
		}

		// Show path popup
		if key.Matches(msg, mPtr.keys.ShowPath) && mPtr.current != "" {
			mPtr.showPathPopup = true
			mPtr.filePath = mPtr.current
			return *mPtr, nil
		}
		if mPtr.showPathPopup && msg.Type == tea.KeyEsc {
			mPtr.showPathPopup = false
			return *mPtr, nil
		}

		// TOC toggle (Ctrl+T)
		if msg.String() == "ctrl+t" && strings.HasSuffix(strings.ToLower(mPtr.current), ".md") {
			mPtr.tocVisible = !mPtr.tocVisible
			if mPtr.tocVisible {
				content := mPtr.currentContent
				mPtr.tocHeadings = parseHeadings(content)
				mPtr.tocCursor = 0
				mPtr.tocOffset = 0
				mPtr.active = "toc"
			} else {
				mPtr.active = "right"
			}
			return *mPtr, nil
		}
		// переключение между панелями по Tab
		if key.Matches(msg, mPtr.keys.Tab) {
			switch mPtr.active {
			case "left":
				if mPtr.tocVisible {
					mPtr.active = "toc"
				} else {
					mPtr.active = "right"
				}
			case "toc":
				mPtr.active = "right"
			case "right":
				mPtr.active = "left"
			}
			return *mPtr, nil
		}

		// left panel navigation
		if mPtr.active == "left" {
			visible := mPtr.height - 4
			if key.Matches(msg, mPtr.keys.Up) {
				if mPtr.cursor > 0 {
					mPtr.cursor--
				}
				if mPtr.cursor < mPtr.leftOffset {
					mPtr.leftOffset = mPtr.cursor
				}
				return *mPtr, nil
			}
			if key.Matches(msg, mPtr.keys.Down) {
				if mPtr.cursor < len(mPtr.files)-1 {
					mPtr.cursor++
				}
				if mPtr.cursor >= mPtr.leftOffset+visible {
					mPtr.leftOffset = mPtr.cursor - visible + 1
				}
				return *mPtr, nil
			}
			if key.Matches(msg, mPtr.keys.Open) {
				mPtr.openSelected()
				mPtr.clampLeftOffset()
				return *mPtr, nil
			}
			if key.Matches(msg, mPtr.keys.Back) {
				parent := filepath.Dir(mPtr.cwd)
				if parent != mPtr.cwd {
					oldPath := mPtr.cwd
					mPtr.cwd = parent
					mPtr.files = loadFiles(mPtr.cwd, mPtr.showHidden)

					base := filepath.Base(oldPath)
					found := false
					for i, f := range mPtr.files {
						if f.isDir && f.name == base {
							mPtr.cursor = i
							found = true
							break
						}
					}
					if !found && len(mPtr.files) > 0 {
						mPtr.cursor = 0
					}
					mPtr.clampLeftOffset()
				}
				return *mPtr, nil
			}
			return *mPtr, nil
		}

		// TOC navigation
		if mPtr.active == "toc" {
			visible := mPtr.height - 4
			if visible < 1 {
				visible = 1
			}

			// handle empty TOC
			if len(mPtr.tocHeadings) == 0 {
				if key.Matches(msg, mPtr.keys.Close) || msg.Type == tea.KeyEnter {
					mPtr.tocVisible = false
					mPtr.active = "right"
				}
				return *mPtr, nil
			}

			if key.Matches(msg, mPtr.keys.Up) {
				if mPtr.tocCursor > 0 {
					mPtr.tocCursor--
				}
				if mPtr.tocCursor < mPtr.tocOffset {
					mPtr.tocOffset = mPtr.tocCursor
				}
				return *mPtr, nil
			}
			if key.Matches(msg, mPtr.keys.Down) {
				if mPtr.tocCursor < len(mPtr.tocHeadings)-1 {
					mPtr.tocCursor++
				}
				if mPtr.tocCursor >= mPtr.tocOffset+visible {
					mPtr.tocOffset = mPtr.tocCursor - visible + 1
				}
				return *mPtr, nil
			}

			// Enter or Right: navigate to heading by rendering the markdown starting from that heading
			if msg.Type == tea.KeyEnter || key.Matches(msg, mPtr.keys.Open) {
				h := mPtr.tocHeadings[mPtr.tocCursor]
				targetLine := h.Line
				if targetLine < 0 {
					targetLine = 0
				}
				lines := strings.Split(mPtr.currentContent, "\n")
				if targetLine >= len(lines) {
					targetLine = 0
				}
				sub := strings.Join(lines[targetLine:], "\n")

				// Render the substring and set viewport content
				out, err := mPtr.renderer.Render(sub)
				if err != nil {
					// if render failed, fall back to plain text (with possible syntax highlight for code files)
					mPtr.vp.SetContent(sub)
				} else {
					mPtr.vp.SetContent(out)
				}

				// mPtr.tocVisible = false
				mPtr.active = "right"
				return *mPtr, nil
			}

			// Esc — close TOC
			if key.Matches(msg, mPtr.keys.Close) {
				// mPtr.tocVisible = false
				mPtr.active = "right"
				return *mPtr, nil
			}

			return *mPtr, nil
		}

		// right panel: allow scrolling etc. via viewport.Update
		if mPtr.active == "right" {
			mPtr.vp, cmd = mPtr.vp.Update(msg)
			return *mPtr, cmd
		}
	}

	return *mPtr, nil
}

func (m *model) openSelected() {
	if len(m.files) == 0 || m.cursor < 0 || m.cursor >= len(m.files) {
		return
	}
	it := m.files[m.cursor]
	if it.isDir {
		oldPath := m.cwd
		m.cwd = it.path
		m.files = loadFiles(m.cwd, m.showHidden)

		if filepath.Dir(oldPath) == m.cwd {
			base := filepath.Base(oldPath)
			for i, f := range m.files {
				if f.isDir && f.name == base {
					m.cursor = i
					break
				}
			}
		} else {
			if len(m.files) > 0 {
				m.cursor = 0
			} else {
				m.cursor = 0
			}
		}

		m.clampLeftOffset()
		return
	}
	data, err := os.ReadFile(it.path)
	if err != nil {
		m.vp.SetContent(fmt.Sprintf("Ошибка чтения: %v", err))
		m.current = ""
		m.currentContent = ""
		return
	}

	content := string(data)
	m.current = it.path
	m.currentContent = content

	// Display logic
	if strings.HasSuffix(strings.ToLower(it.path), ".md") {
		out, err := m.renderer.Render(content)
		if err != nil {
			m.vp.SetContent(content)
		} else {
			m.vp.SetContent(out)
		}
	} else {
		displayContent := content
		if strings.HasSuffix(strings.ToLower(it.path), ".go") || strings.HasSuffix(strings.ToLower(it.path), ".py") {
			if highlighted, err := highlightSyntax(content, it.path); err == nil {
				displayContent = highlighted
			}
		}
		m.vp.SetContent(displayContent)
	}

	m.active = "right"
}

// ---------------- view ----------------

func (m model) pathPopup() string {
	pathText := fmt.Sprintf("Путь к файлу:\n%s", m.filePath)
	pathStyle := lipgloss.NewStyle().
		Width(60).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, pathStyle.Render(pathText))
}

func (m model) helpPopup() string {
	helpText := `Справка

↑ / ↓      — перемещение по списку файлов / TOC
→          — открыть файл / перейти в директорию / в TOC — перейти к заголовку
←          — уровень выше (или назад)
.          — показать/скрыть скрытые файлы
?          — показать/скрыть справку
Ctrl+Q     — выйти
Ctrl+P     — показать путь к файлу
Ctrl+T     — показать/скрыть TOC (для .md)`
	helpStyle := lipgloss.NewStyle().
		Width(60).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpStyle.Render(helpText))
}

// truncateString обрезает строку до заданной ширины и добавляет "..." в конце
func (m model) truncateString(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	ell := "..."
	ellW := runewidth.StringWidth(ell)
	target := width - ellW
	if target < 1 {
		return runewidth.Truncate(s, width, "")
	}
	return runewidth.Truncate(s, target, "") + ell
}

func (m model) View() string {
	if !m.ready {
		return ""
	}
	if m.showPathPopup {
		return m.pathPopup()
	}
	if m.showHelp {
		return m.helpPopup()
	}

	if m.tocVisible {
		// Draw TOC in left panel
		innerH := m.height - 4
		if innerH < 1 {
			innerH = 1
		}
		leftStyle := lipgloss.NewStyle().Width(m.leftWidth).Height(innerH+2).MarginTop(1).Padding(0, 1).Border(lipgloss.RoundedBorder())
		rightW := m.width - m.leftWidth - 4
		if rightW < 20 {
			rightW = 20
		}
		rightStyle := lipgloss.NewStyle().Width(rightW).Height(innerH+1).MarginTop(1).Padding(0, 0).Border(lipgloss.RoundedBorder())

		if m.active == "left" {
			leftStyle = leftStyle.BorderForeground(lipgloss.Color("205"))
			rightStyle = rightStyle.BorderForeground(lipgloss.Color("240"))
		} else {
			leftStyle = leftStyle.BorderForeground(lipgloss.Color("240"))
			rightStyle = rightStyle.BorderForeground(lipgloss.Color("205"))
		}

		visible := innerH
		if visible < 1 {
			visible = 1
		}
		start := m.tocOffset
		end := start + visible
		if start < 0 {
			start = 0
		}
		if end > len(m.tocHeadings) {
			end = len(m.tocHeadings)
		}

		var b strings.Builder
		for i := start; i < end; i++ {
			h := m.tocHeadings[i]
			indent := strings.Repeat("  ", h.Level-1)
			contentWidth := m.leftWidth - 4
			maxLen := contentWidth - len(indent)
			if maxLen < 1 {
				maxLen = 1
			}
			title := m.truncateString(h.Title, maxLen)
			line := fmt.Sprintf("%s%s", indent, title)
			if i == m.tocCursor {
				line = lipgloss.NewStyle().
					Foreground(lipgloss.Color("205")).
					Bold(true).
					Width(contentWidth).
					MaxWidth(contentWidth).
					Render(line)
			}
			b.WriteString(line + "\n")
		}
		written := end - start
		for i := 0; i < visible-written; i++ {
			b.WriteString("\n")
		}

		tocStyle := lipgloss.NewStyle().
			Width(m.leftWidth).
			Height(innerH+2).
			MarginTop(1).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205"))

		toc := tocStyle.Render(b.String())
		right := rightStyle.Render(m.vp.View())
		return "\n" + lipgloss.JoinHorizontal(lipgloss.Bottom, toc, right)
	}

	// обычная файловая панель слева
	innerH := m.height - 4
	if innerH < 1 {
		innerH = 1
	}
	leftStyle := lipgloss.NewStyle().Width(m.leftWidth).Height(innerH+2).MarginTop(1).Padding(0, 1).Border(lipgloss.RoundedBorder())
	rightW := m.width - m.leftWidth - 4
	if rightW < 20 {
		rightW = 20
	}
	rightStyle := lipgloss.NewStyle().Width(rightW).Height(innerH+1).MarginTop(1).Padding(0, 0).Border(lipgloss.RoundedBorder())

	if m.active == "left" {
		leftStyle = leftStyle.BorderForeground(lipgloss.Color("205"))
		rightStyle = rightStyle.BorderForeground(lipgloss.Color("240"))
	} else {
		leftStyle = leftStyle.BorderForeground(lipgloss.Color("240"))
		rightStyle = rightStyle.BorderForeground(lipgloss.Color("205"))
	}

	var leftBuilder strings.Builder
	start := m.leftOffset
	end := start + innerH
	if end > len(m.files) {
		end = len(m.files)
	}
	for i := start; i < end; i++ {
		f := m.files[i]
		line := f.name
		line = m.truncateString(line, m.leftWidth-2)
		if i == m.cursor && m.active == "left" {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render(line)
		}
		if f.isDir {
			line = line + "/"
		}
		leftBuilder.WriteString(line + "\n")
	}
	written := end - start
	for i := 0; i < innerH-written; i++ {
		leftBuilder.WriteString("\n")
	}
	left := leftStyle.Render(leftBuilder.String())

	right := rightStyle.Render(m.vp.View())
	return "\n" + lipgloss.JoinHorizontal(lipgloss.Bottom, left, right)
}

// ---------------- main ----------------

func main() {
	if err := tea.NewProgram(initialModel(), tea.WithAltScreen()).Start(); err != nil {
		fmt.Println("Ошибка запуска:", err)
		os.Exit(1)
	}
}
