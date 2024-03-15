package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
)

type model struct {
	viewport    viewport.Model
	messages    []string
	textarea    textarea.Model
	senderStyle lipgloss.Style
	err         error
}

type (
	errMsg error
)

func checkAPIConnection() string {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY is not set")
	}

	_, err := http.NewRequest("GET", "https://api.anthropic.com/v1/ping", nil)
	if err != nil {
		log.Fatalf("Error creating request: %v", err)
	}

	return "API is up and running"
}

func initialModel() model {
	ta := textarea.New()
	ta.Placeholder = "Send a message..."
	ta.Focus()

	ta.Prompt = "┃ "
	ta.CharLimit = 280

	ta.SetWidth(30)
	ta.SetHeight(3)

	// Remove cursor line styling
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ta.ShowLineNumbers = false

	vp := viewport.New(30, 5)
	vp.SetContent(checkAPIConnection())

	ta.KeyMap.InsertNewline.SetEnabled(false)

	return model{
		textarea:    ta,
		messages:    []string{},
		viewport:    vp,
		senderStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		err:         nil,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

type MessageToSend struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func ConstructUserMessage(content string) MessageToSend {
	return MessageToSend{
		Role:    "user",
		Content: content,
	}
}

func (m model) constructJsonBody(content string) ([]byte, error) {
	messages := []MessageToSend{
		ConstructUserMessage(content),
	}

	body, err := json.Marshal(map[string]interface{}{
		"model":      "claude-3-opus-20240229",
		"max_tokens": 4096,
		"messages":   messages,
	})
	if err != nil {
		log.Fatalf("Error marshaling JSON: %v", err)
		return nil, err
	}

	return body, nil
}

func (m model) callClaudeAPI(apiKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{}
	return client.Do(req)
}

func (m model) processAPIResponse(resp *http.Response, resultChan chan string) {
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading response body: %v", err)
			return
		}
		log.Printf("API error: %s", string(bodyBytes))
		return
	}

	scanner := bufio.NewReader(resp.Body)
	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break // End of the stream
			}
			log.Printf("Error reading response: %v", err)
			return
		}

		line = strings.TrimSpace(line)
		if line != "" {
			resultChan <- line
		}
	}
}

func (m model) CallClaude(content string, resultChan chan string) tea.Cmd {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	return func() tea.Msg {
		body, err := m.constructJsonBody(content)
		if err != nil {
			return errMsg(err)
		}

		resp, err := m.callClaudeAPI(apiKey, body)
		if err != nil {
			return errMsg(err)
		}

		go m.processAPIResponse(resp, resultChan)
		return nil
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			fmt.Println(m.textarea.Value())
			return m, tea.Quit
		case tea.KeyEnter:
			m.messages = append(m.messages, m.senderStyle.Render("You: ")+m.textarea.Value())
			m.viewport.SetContent(strings.Join(m.messages, "\n"))

			resultChan := make(chan string)
			go func() {
				for result := range resultChan {
					m.messages = append(m.messages, m.senderStyle.Render("Claude: ")+result)
					m.viewport.SetContent(strings.Join(m.messages, "\n"))
					m.viewport.GotoBottom()
				}
			}()

			m.textarea.Reset()
			m.viewport.GotoBottom()
			return m, m.CallClaude(m.textarea.Value(), resultChan)
		}

	// We handle errors just like any other message
	case errMsg:
		m.err = msg
		return m, nil
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m model) View() string {
	return fmt.Sprintf(
		"%s\n\n%s",
		m.viewport.View(),
		m.textarea.View(),
	) + "\n\n"
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	p := tea.NewProgram(initialModel())

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
