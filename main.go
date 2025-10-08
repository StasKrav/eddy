package main

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/alecthomas/chroma/v2"
    "github.com/alecthomas/chroma/v2/formatters"
    "github.com/alecthomas/chroma/v2/lexers"
    "github.com/alecthomas/chroma/v2/styles"
    "github.com/charmbracelet/bubbles/key"
    "github.com/charmbracelet/bubbles/textarea"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/glamour"
    "github.com/charmbracelet/lipgloss"
)

// ---------------- key bindings ----------------

type keyMap struct {
    Up, Down                key.Binding
    Open, Back              key.Binding
    Save, NewFile, Delete   key.Binding
    ToggleHidden            key.Binding
    SwitchLeft, SwitchRight key.Binding
    Tab, Help, Close        key.Binding
    Quit                    key.Binding
    ShowPath                key.Binding // Клавиша для отображения пути
}

func newKeyMap() keyMap {
    return keyMap{
        Up:          key.NewBinding(key.WithKeys("up")),
        Down:        key.NewBinding(key.WithKeys("down")),
        Open:        key.NewBinding(key.WithKeys("right")),
        Back:        key.NewBinding(key.WithKeys("left")),
        Save:        key.NewBinding(key.WithKeys("ctrl+s")),
        NewFile:     key.NewBinding(key.WithKeys("ctrl+n")),
        Delete:      key.NewBinding(key.WithKeys("ctrl+d")),
        ToggleHidden: key.NewBinding(key.WithKeys(".")),
        SwitchLeft:  key.NewBinding(key.WithKeys("ctrl+left")),
        SwitchRight: key.NewBinding(key.WithKeys("ctrl+right")),
        Tab:         key.NewBinding(key.WithKeys("tab")),
        Help:        key.NewBinding(key.WithKeys("?")),
        Close:       key.NewBinding(key.WithKeys("esc")),
        Quit:        key.NewBinding(key.WithKeys("ctrl+q")),
        ShowPath:    key.NewBinding(key.WithKeys("ctrl+p")), // Изменена клавиша "p" на "Ctrl+p" для отображения пути
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

    cwd        string   // current working directory
    files      []fileItem // files in cwd
    cursor     int      // which file is selected
    leftOffset int      // offset for scrolling the left pane
    showHidden bool     // show hidden files

    ta       textarea.Model // text area for editing files
    vp       viewport.Model // viewport for previewing files
    renderer *glamour.TermRenderer
    current  string // current file being edited
    mode     string // "edit" or "preview"

    active   string // "left" or "right"
    showHelp bool
    keys     keyMap

    creatingFile bool
    newFileName  string

    deletingFile bool
    deleteTarget *fileItem

    showPathPopup bool   // Флаг для отображения popup с путем
    filePath      string // Строка для хранения пути файла
}

// ---------------- initialization ----------------

func initialModel() model {
    ta := textarea.New()
    ta.Placeholder = " ** -> ** Откроет файл в панели редактора"
    ta.ShowLineNumbers = true
    ta.Cursor.Style = lipgloss.NewStyle().UnsetForeground()
    ta.Blur()

    vp := viewport.New(0, 0)
    renderer, _ := glamour.NewTermRenderer(
        glamour.WithAutoStyle(),
        glamour.WithWordWrap(80),
    )

    cwd, _ := os.Getwd()       // current working directory
    files := loadFiles(cwd, false) // load files in cwd

    return model{
        width:      100,
        height:     30,
        leftWidth:  36,
        cwd:        cwd,
        files:      files,
        cursor:     0,
        leftOffset: 0,
        showHidden: false,
        ta:         ta,
        vp:         vp,
        renderer:   renderer,
        current:  "",
        mode:       "edit",
        active:   "left",
        showHelp:   false,
        keys:       newKeyMap(),
        showPathPopup: false, // Изначально не показываем popup
        filePath:      "",
    }
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
    // visible - количество видимых элементов в списке файлов
    visible := m.height - 4

    // Если файлов для отображения меньше, чем видимых элементов,
    // то leftOffset всегда должен быть 0.
    if len(m.files) <= visible {
        m.leftOffset = 0
        return
    }

    // Если текущий курсор находится выше области видимости,
    // устанавливаем leftOffset равным курсору.
    if m.cursor < m.leftOffset {
        m.leftOffset = m.cursor
    }

    // Если текущий курсор находится ниже области видимости,
    // устанавливаем leftOffset так, чтобы курсор был виден.
    if m.cursor >= m.leftOffset+visible {
        m.leftOffset = m.cursor - visible + 1
    }

    // leftOffset не может быть меньше 0.
    if m.leftOffset < 0 {
        m.leftOffset = 0
    }

    // leftOffset не может быть больше, чем (количество файлов - количество видимых элементов).
    if m.leftOffset > len(m.files)-visible {
        m.leftOffset = len(m.files) - visible
    }
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
                if m.files[0].name == ".." && len(m.files) > 1 {
                    m.cursor = 1
                }
            } else {
                m.cursor = 0
            }
        }

        m.clampLeftOffset()
        return
    }
    data, err := os.ReadFile(it.path)
    if err != nil {
        m.ta.SetValue(fmt.Sprintf("Ошибка чтения: %v", err))
        m.current = ""
        return
    }

    content := string(data)
    highlightedContent := content
    if strings.HasSuffix(strings.ToLower(it.path), ".go") || strings.HasSuffix(strings.ToLower(it.path), ".py") {
        if highlighted, err := highlightSyntax(content, it.path); err == nil {
            highlightedContent = highlighted
        }
    }

    m.ta.SetValue(content) // В редакторе показываем оригинальный текст без подсветки
    m.current = it.path

    if strings.HasSuffix(strings.ToLower(it.path), ".md") {
        out, err := m.renderer.Render(content)
        if err != nil {
            m.vp.SetContent(highlightedContent)
        } else {
            m.vp.SetContent(out)
        }
    } else {
        m.vp.SetContent(highlightedContent)
    }
    m.mode = "edit"
    m.active = "right"
    m.ta.Focus()
}

func (m *model) saveCurrent() {
    if m.current == "" {
        return
    }
    _ = os.WriteFile(m.current, []byte(m.ta.Value()), 0644)

    content := m.ta.Value()
    displayContent := content
    if strings.HasSuffix(strings.ToLower(m.current), ".go") || strings.HasSuffix(strings.ToLower(m.current), ".py") {
        if highlighted, err := highlightSyntax(content, m.current); err == nil {
            displayContent = highlighted
        }
    }

    if strings.HasSuffix(strings.ToLower(m.current), ".md") {
        out, err := m.renderer.Render(content)
        if err != nil {
            m.vp.SetContent(displayContent)
        } else {
            m.vp.SetContent(out)
        }
    } else {
        m.vp.SetContent(displayContent)
    }
}

// ---------------- update ----------------

func (m model) Init() tea.Cmd {
    return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmd tea.Cmd

    mPtr := &m

    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        mPtr.width, mPtr.height = msg.Width, msg.Height
        rightW := mPtr.width - mPtr.leftWidth - 4
        if rightW < 20 {
            rightW = 20
        }
        mPtr.ta.SetWidth(rightW - 1)       // Увеличил ширину, чтобы скрыть полосу прокрутки
        mPtr.ta.SetHeight(mPtr.height - 2) // Увеличил высоту, чтобы скрыть полосу прокрутки
        mPtr.vp.Width = rightW - 1
        mPtr.vp.Height = mPtr.height - 2
        mPtr.clampLeftOffset() // Важно вызывать clampLeftOffset после изменения размеров окна
        return *mPtr, nil

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

        // --- Новый файл (Ctrl+N) ---
        if key.Matches(msg, mPtr.keys.NewFile) {
            mPtr.creatingFile = true
            mPtr.newFileName = ""
            mPtr.active = "newfile"
            return *mPtr, nil
        }
        if mPtr.creatingFile {
            if msg.Type == tea.KeyEnter {
                if mPtr.newFileName != "" {
                    newPath := filepath.Join(mPtr.cwd, mPtr.newFileName)
                    _ = os.WriteFile(newPath, []byte(""), 0644)
                    mPtr.files = loadFiles(mPtr.cwd, mPtr.showHidden)
                    mPtr.current = newPath
                    mPtr.ta.SetValue("")
                    mPtr.mode = "edit"
                    mPtr.active = "right"
                    mPtr.ta.Focus()
                }
                mPtr.creatingFile = false
                return *mPtr, nil
            }
            if msg.Type == tea.KeyEsc {
                mPtr.creatingFile = false
                mPtr.active = "left"
                return *mPtr, nil
            }
            if msg.Type == tea.KeyBackspace && len(mPtr.newFileName) > 0 {
                mPtr.newFileName = mPtr.newFileName[:len(mPtr.newFileName)-1]
                return *mPtr, nil
            }
            if len(msg.String()) == 1 && msg.Runes != nil {
                mPtr.newFileName += msg.String()
                return *mPtr, nil
            }
        }

        // --- Удаление файла (Ctrl+D) ---
        if key.Matches(msg, mPtr.keys.Delete) && mPtr.active == "left" && len(mPtr.files) > 0 {
            it := mPtr.files[mPtr.cursor]
            if it.name != ".." {
                mPtr.deletingFile = true
                mPtr.deleteTarget = &it
            }
            return *mPtr, nil
        }
        if mPtr.deletingFile {
            if msg.String() == "y" || msg.String() == "Y" {
                if mPtr.deleteTarget != nil {
                    oldCursor := mPtr.cursor
                    err := os.Remove(mPtr.deleteTarget.path)
                    if err != nil {
                        mPtr.ta.SetValue(fmt.Sprintf("Ошибка удаления: %v", err))
                    } else {
                        mPtr.files = loadFiles(mPtr.cwd, mPtr.showHidden)
                        if oldCursor >= len(mPtr.files) {
                            mPtr.cursor = len(mPtr.files) - 1
                        } else {
                            mPtr.cursor = oldCursor
                        }
                        if mPtr.cursor < 0 || len(mPtr.files) == 0 {
                            mPtr.cursor = 0
                        }
                        mPtr.clampLeftOffset()
                    }
                }
                mPtr.deletingFile = false
                mPtr.deleteTarget = nil
                return *mPtr, nil
            }
            if msg.String() == "n" || msg.Type == tea.KeyEsc {
                mPtr.deletingFile = false
                mPtr.deleteTarget = nil
                return *mPtr, nil
            }
            return *mPtr, nil
        }

        // глобальные
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
        if key.Matches(msg, mPtr.keys.SwitchLeft) {
            mPtr.active = "left"
            mPtr.ta.Blur()
            return *mPtr, nil
        }
        if key.Matches(msg, mPtr.keys.SwitchRight) {
            mPtr.active = "right"
            if mPtr.mode == "edit" {
                mPtr.ta.Focus()
            }
            return *mPtr, nil
        }

        // Обработка нажатия клавиши "Ctrl+p"
        if key.Matches(msg, mPtr.keys.ShowPath) && mPtr.current != "" {
            mPtr.showPathPopup = true      // Показываем popup
            mPtr.filePath = mPtr.current // Установим путь к текущему файлу
            return *mPtr, nil
        }

        // Закрытие popup с путем по нажатию Esc
        if mPtr.showPathPopup && msg.Type == tea.KeyEsc {
            mPtr.showPathPopup = false // Закрываем popup
            return *mPtr, nil
        }

        // левая панель
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

        // правая панель
        if key.Matches(msg, mPtr.keys.Save) {
            mPtr.saveCurrent()
            return *mPtr, nil
        }
        if key.Matches(msg, mPtr.keys.Tab) {
            if mPtr.current == "" {
                return *mPtr, nil
            }
            if mPtr.mode == "edit" {
                content := mPtr.ta.Value()
                displayContent := content
                if strings.HasSuffix(strings.ToLower(mPtr.current), ".go") || strings.HasSuffix(strings.ToLower(mPtr.current), ".py") {
                    if highlighted, err := highlightSyntax(content, mPtr.current); err == nil {
                        displayContent = highlighted
                    }
                }

                if strings.HasSuffix(strings.ToLower(mPtr.current), ".md") {
                    out, err := mPtr.renderer.Render(content)
                    if err != nil {
                        mPtr.vp.SetContent(displayContent)
                    } else {
                        mPtr.vp.SetContent(out)
                    }
                } else {
                    mPtr.vp.SetContent(displayContent)
                }
                mPtr.mode = "preview"
                mPtr.ta.Blur()
            } else {
                mPtr.mode = "edit"
                mPtr.ta.Focus()
            }
            return *mPtr, nil
        }
        if mPtr.active == "right" {
            if mPtr.mode == "edit" {
                mPtr.ta, cmd = mPtr.ta.Update(msg)
                return *mPtr, cmd
            }
            if mPtr.mode == "preview" {
                mPtr.vp, cmd = mPtr.vp.Update(msg)
                return *mPtr, cmd
            }
        }
    }

    return *mPtr, nil
}

// ---------------- view ----------------

// pathPopup - Отображает popup окно с путем к файлу
func (m model) pathPopup() string {
    pathText := fmt.Sprintf("Путь к файлу:\n%s", m.filePath) // Используем filePath из модели
    pathStyle := lipgloss.NewStyle().
        Width(60).
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("205")).
        Padding(1, 2)
    return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, pathStyle.Render(pathText))
}

func (m model) helpPopup() string {
    helpText := `Справка

↑ / ↓      — перемещение по списку файлов
→          — открыть файл / войти в директорию
←          — уровень выше (или назад)
.          — показать/скрыть скрытые файлы
Ctrl+N     — создать новый файл
Ctrl+D     — удалить файл
Tab        — переключить Редактор ↔ Предпросмотр
Ctrl+S     — сохранить файл
Ctrl+←/→   — переключить активную панель
?          — показать/скрыть справку
Ctrl+Q     — выйти
Ctrl+P     — показать путь к файлу`
    helpStyle := lipgloss.NewStyle().
        Width(60).
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("205")).
        Padding(1, 2)
    return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpStyle.Render(helpText))
}

// truncateString обрезает строку до заданной ширины и добавляет "..." в конце
func (m model) truncateString(s string, width int) string {
    if len(s) > width {
        return s[:width-3] + "..."
    }
    return s
}

func (m model) View() string {
    if m.showPathPopup {
        return m.pathPopup() // Отображаем popup с путем, если showPathPopup true
    }
    if m.showHelp {
        return m.helpPopup()
    }
    if m.creatingFile {
        prompt := lipgloss.NewStyle().
            Border(lipgloss.RoundedBorder()).
            BorderForeground(lipgloss.Color("205")).
            Padding(1, 2).
            Render("Введите имя файла: " + m.newFileName)
        return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, prompt)
    }
    if m.deletingFile && m.deleteTarget != nil {
        prompt := lipgloss.NewStyle().
            Border(lipgloss.RoundedBorder()).
            BorderForeground(lipgloss.Color("1")).
            Padding(1, 2).
            Render(fmt.Sprintf("Удалить «%s»? (y/n)", m.deleteTarget.name))
        return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, prompt)
    }

    innerH := m.height - 4
    // Применяем стиль с рамкой к левой панели. Рамка левой панели такая же высоты, что и рамка правой панели
    leftStyle := lipgloss.NewStyle().Width(m.leftWidth).Height(innerH + 2).MarginTop(1).Padding(0, 1).Border(lipgloss.RoundedBorder())
    rightW := m.width - m.leftWidth - 4
    if rightW < 20 {
        rightW = 20
    }
    // Изменяем стиль правой панели, чтобы скрыть полосу прокрутки
    rightStyle := lipgloss.NewStyle().Width(rightW).Height(innerH + 1).MarginTop(1).Padding(0, 0).Border(lipgloss.RoundedBorder())

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
        // Обрезаем имя файла, чтобы оно не превышало ширину левой панели
        line = m.truncateString(line, m.leftWidth-2) // -2 для отступа/padding
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

    var right string
    if m.active == "right" && m.mode == "preview" {
        right = rightStyle.Render(m.vp.View())
    } else {
        right = rightStyle.Render(m.ta.View())
    }

    return "\n" + lipgloss.JoinHorizontal(lipgloss.Bottom, left, right)
}

// ---------------- main ----------------

func main() {
    if err := tea.NewProgram(initialModel(), tea.WithAltScreen()).Start(); err != nil {
        fmt.Println("Ошибка запуска:", err)
        os.Exit(1)
    }
}
