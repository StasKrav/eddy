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
}

func newKeyMap() keyMap {
    return keyMap{
        Up:           key.NewBinding(key.WithKeys("up")),
        Down:         key.NewBinding(key.WithKeys("down")),
        Open:         key.NewBinding(key.WithKeys("right")),
        Back:         key.NewBinding(key.WithKeys("left")),
        Save:         key.NewBinding(key.WithKeys("ctrl+s")),
        NewFile:      key.NewBinding(key.WithKeys("ctrl+n")),
        Delete:       key.NewBinding(key.WithKeys("ctrl+d")),
        ToggleHidden: key.NewBinding(key.WithKeys(".")),
        SwitchLeft:   key.NewBinding(key.WithKeys("ctrl+left")),
        SwitchRight:  key.NewBinding(key.WithKeys("ctrl+right")),
        Tab:          key.NewBinding(key.WithKeys("tab")),
        Help:         key.NewBinding(key.WithKeys("?")),
        Close:        key.NewBinding(key.WithKeys("esc")),
        Quit:         key.NewBinding(key.WithKeys("ctrl+q")),
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

    cwd        string
    files      []fileItem
    cursor     int
    leftOffset int
    showHidden bool

    ta       textarea.Model
    vp       viewport.Model
    renderer *glamour.TermRenderer
    current  string
    mode     string // "edit" or "preview"

    active   string // "left" or "right"
    showHelp bool
    keys     keyMap

    creatingFile bool
    newFileName  string

    deletingFile bool
    deleteTarget *fileItem
}

// ---------------- initialization ----------------

func initialModel() model {
    ta := textarea.New()
    ta.Placeholder = "Откройте файл в левой панели"
    ta.ShowLineNumbers = true
    ta.Cursor.Style = lipgloss.NewStyle().UnsetForeground()
    // Попытка отключить полосу прокрутки
    // ta.SetShowScrollbar(false) // Закомментировано, так как не уверен в существовании такого метода
    ta.Blur()

    vp := viewport.New(0, 0)
    renderer, _ := glamour.NewTermRenderer(
        glamour.WithAutoStyle(),
        glamour.WithWordWrap(80),
    )

    cwd, _ := os.Getwd()
    files := loadFiles(cwd, false)

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
        current:    "",
        mode:       "edit",
        active:     "left",
        showHelp:   false,
        keys:       newKeyMap(),
    }
}

// ---------------- helpers ----------------

func loadFiles(dir string, showHidden bool) []fileItem {
    entries, err := os.ReadDir(dir)
    if err != nil {
        return nil
    }
    var items []fileItem
    //Удаляем добавление ".." в начало списка файлов
    //if parent := filepath.Dir(dir); parent != dir {
    //  items = append(items, fileItem{name: "..", path: parent, isDir: true})
    //}
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
    // Определяем лексер по расширению файла
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

    // Получаем итератор токенов
    iterator, err := lexer.Tokenise(nil, content)
    if err != nil {
        return content, err
    }

    // Используем терминальный форматтер и стиль
    formatter := formatters.Get("terminal")
    if formatter == nil {
        return content, nil
    }

    style := styles.Get("github")
    if style == nil {
        style = styles.Fallback
    }

    // Форматируем в строку
    var buf strings.Builder
    err = formatter.Format(&buf, style, iterator)
    if err != nil {
        return content, err
    }

    return buf.String(), nil
}

// --- исправленный clamp ---
func (m *model) clampLeftOffset() {
    // Проверяем, что у нас есть файлы для отображения
    if len(m.files) == 0 {
        m.leftOffset = 0
        m.cursor = 0
        return
    }

    visible := m.height - 4
    if visible < 1 {
        visible = 1
    }

    // Проверяем границы курсора
    if m.cursor < 0 {
        m.cursor = 0
    } else if m.cursor >= len(m.files) {
        m.cursor = len(m.files) - 1
    }

    // Корректируем leftOffset относительно позиции курсора
    if m.cursor < m.leftOffset {
        m.leftOffset = m.cursor
    } else if m.cursor >= m.leftOffset+visible {
        m.leftOffset = m.cursor - visible + 1
    }

    // Проверяем границы leftOffset
    if m.leftOffset < 0 {
        m.leftOffset = 0
    }

    maxOffset := 0
    if len(m.files) > visible {
        maxOffset = len(m.files) - visible
    }
    if m.leftOffset > maxOffset {
        m.leftOffset = maxOffset
    }

    // Дополнительная проверка для предотвращения подпрыгивания
    if m.leftOffset < 0 {
        m.leftOffset = 0
    }
    // Убеждаемся, что leftOffset не превышает количество файлов
    if m.leftOffset >= len(m.files) {
        m.leftOffset = len(m.files) - 1
    }
    // Если leftOffset отрицательный, устанавливаем в 0
    if m.leftOffset < 0 {
        m.leftOffset = 0
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

        // --- фикс: восстановление курсора при возврате в родительскую папку ---
        if filepath.Dir(oldPath) == m.cwd {
            // ищем директорию, из которой пришли
            base := filepath.Base(oldPath)
            for i, f := range m.files {
                if f.isDir && f.name == base {
                    m.cursor = i
                    break
                }
            }
        } else {
            // При входе в новую директорию - устанавливаем курсор на первый элемент
            // но проверяем, есть ли файлы
            if len(m.files) > 0 {
                m.cursor = 0
                // Если есть "..", ставим курсор на первый файл после него
                if m.files[0].name == ".." && len(m.files) > 1 {
                    m.cursor = 1
                }
            } else {
                m.cursor = 0
            }
        }

        // Вместо сброса leftOffset в 0, оставляем его как есть
        // и позволяем clampLeftOffset() вычислить правильное значение
        m.clampLeftOffset()
        return
    }
    data, err := os.ReadFile(it.path)
    if err != nil {
        m.ta.SetValue(fmt.Sprintf("Ошибка чтения: %v", err))
        m.current = ""
        return
    }

    // Применяем подсветку синтаксиса для Go и Python файлов
    content := string(data)
    highlightedContent := content
    if strings.HasSuffix(strings.ToLower(it.path), ".go") || strings.HasSuffix(strings.ToLower(it.path), ".py") {
        if highlighted, err := highlightSyntax(content, it.path); err == nil {
            highlightedContent = highlighted
        }
    }

    m.ta.SetValue(content) // В редакторе показываем оригинальный текст без подсветки
    m.current = it.path

    // Для предпросмотра используем подсвеченный текст для Go/Python файлов
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

    // Применяем подсветку синтаксиса для Go и Python файлов
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

func (m model) Init() tea.Cmd { return nil }

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
                        // Перезагружаем список файлов
                        mPtr.files = loadFiles(mPtr.cwd, mPtr.showHidden)
                        // Подстраиваем курсор после удаления
                        if oldCursor >= len(mPtr.files) {
                            mPtr.cursor = len(mPtr.files) - 1
                        } else {
                            mPtr.cursor = oldCursor
                        }
                        // Проверяем, что курсор в допустимых пределах
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
            // Вместо сброса leftOffset в 0, позволяем clampLeftOffset() вычислить правильное значение
            // Проверяем, что курсор в допустимых пределах
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

                    // --- фикс: вернуться к директории, из которой пришли ---
                    base := filepath.Base(oldPath)
                    found := false
                    for i, f := range mPtr.files {
                        if f.isDir && f.name == base {
                            mPtr.cursor = i
                            found = true
                            break
                        }
                    }
                    // Если не нашли директорию, ставим курсор на первый элемент
                    if !found && len(mPtr.files) > 0 {
                        mPtr.cursor = 0
                    }
                    // Вместо сброса leftOffset в 0, оставляем его как есть
                    // и позволяем clampLeftOffset() вычислить правильное значение
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
                // Применяем подсветку синтаксиса для Go и Python файлов
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
Ctrl+Q     — выйти`
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
    leftStyle := lipgloss.NewStyle().Width(m.leftWidth).Height(innerH).MarginTop(1).Padding(0, 1)
    rightW := m.width - m.leftWidth - 4
    if rightW < 20 {
        rightW = 20
    }
    // Изменяем стиль правой панели, чтобы скрыть полосу прокрутки
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
