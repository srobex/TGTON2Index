package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/fatih/color"
	"github.com/yourname/hyper-sniper-indexer/internal/config"
	"github.com/yourname/hyper-sniper-indexer/internal/detector"
	"go.uber.org/zap"
)

const (
	tonViewerBase  = "https://tonviewer.com/"
	tonscanBase    = "https://tonscan.org/address/"
	dexScreenerURL = "https://dexscreener.com/ton/"
)

// Notifier отправляет события в TG, webhook и консоль.
type Notifier struct {
	tgToken    string
	tgChatID   string
	webhookURL string
	logger     *zap.Logger
	httpClient *http.Client
}

// New создаёт нотификатор. TG не запускается, если токен пустой.
func New(cfg *config.Config, logger *zap.Logger) *Notifier {
	return &Notifier{
		tgToken:    cfg.Notifier.TgBotToken,
		tgChatID:   cfg.Notifier.TgChatID,
		webhookURL: cfg.Notifier.WebhookURL,
		logger:     logger,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify отправляет сообщение во все доступные каналы.
func (n *Notifier) Notify(ctx context.Context, meta *detector.Metadata) {
	// Консольный вывод (всегда)
	n.console(meta)

	// Telegram (если настроен)
	if n.tgToken != "" && n.tgChatID != "" {
		if err := n.telegram(ctx, meta); err != nil {
			n.logger.Warn("ошибка отправки в Telegram", zap.Error(err))
		}
	}

	// Webhook (если настроен)
	if n.webhookURL != "" {
		if err := n.webhook(ctx, meta); err != nil {
			n.logger.Warn("ошибка отправки в webhook", zap.Error(err))
		}
	}
}

// console выводит цветное сообщение в консоль.
func (n *Notifier) console(meta *detector.Metadata) {
	green := color.New(color.FgHiGreen, color.Bold)
	cyan := color.New(color.FgCyan)
	yellow := color.New(color.FgYellow)
	white := color.New(color.FgWhite)

	fmt.Println()
	green.Println("╔══════════════════════════════════════════════════════════════╗")
	green.Println("║           🚀 НОВЫЙ JETTON MINTER ОБНАРУЖЕН! 🚀              ║")
	green.Println("╚══════════════════════════════════════════════════════════════╝")

	if meta.Name != "" || meta.Symbol != "" {
		yellow.Printf("  Название: %s (%s)\n", meta.Name, meta.Symbol)
	}

	white.Printf("  Адрес:    %s\n", meta.Address)
	cyan.Printf("  Тип:      %s\n", meta.MinterType)
	white.Printf("  CodeHash: %s\n", meta.CodeHash)

	if meta.TotalSupply != "" {
		white.Printf("  Supply:   %s\n", meta.TotalSupply)
	}

	fmt.Println()
	cyan.Printf("  📎 Tonviewer:    %s%s\n", tonViewerBase, meta.Address)
	cyan.Printf("  📎 Tonscan:      %s%s\n", tonscanBase, meta.Address)
	cyan.Printf("  📎 DexScreener:  %s%s\n", dexScreenerURL, meta.Address)

	white.Printf("\n  ⏱️  Время: %s\n", meta.Timestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Println()
}

// telegram отправляет сообщение в Telegram.
func (n *Notifier) telegram(ctx context.Context, meta *detector.Metadata) error {
	// Формируем красивое сообщение
	var text string

	if meta.Name != "" || meta.Symbol != "" {
		text = fmt.Sprintf(
			"🚀 *НОВЫЙ JETTON MINTER*\n\n"+
				"📝 *Название:* %s\n"+
				"🏷️ *Тикер:* %s\n"+
				"📍 *Адрес:* `%s`\n"+
				"🔧 *Тип:* %s\n"+
				"🔗 *CodeHash:* `%s`\n\n"+
				"🔍 [Tonviewer](%s%s) | [Tonscan](%s%s) | [DexScreener](%s%s)\n\n"+
				"⏱️ %s",
			escapeMarkdown(meta.Name),
			escapeMarkdown(meta.Symbol),
			meta.Address,
			escapeMarkdown(meta.MinterType),
			meta.CodeHash[:16]+"...",
			tonViewerBase, meta.Address,
			tonscanBase, meta.Address,
			dexScreenerURL, meta.Address,
			meta.Timestamp.Format("15:04:05 MST"),
		)
	} else {
		text = fmt.Sprintf(
			"🚀 *НОВЫЙ JETTON MINTER*\n\n"+
				"📍 *Адрес:* `%s`\n"+
				"🔧 *Тип:* %s\n"+
				"🔗 *CodeHash:* `%s`\n\n"+
				"🔍 [Tonviewer](%s%s) | [Tonscan](%s%s)\n\n"+
				"⏱️ %s",
			meta.Address,
			escapeMarkdown(meta.MinterType),
			meta.CodeHash[:16]+"...",
			tonViewerBase, meta.Address,
			tonscanBase, meta.Address,
			meta.Timestamp.Format("15:04:05 MST"),
		)
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.tgToken)
	data := url.Values{}
	data.Set("chat_id", n.tgChatID)
	data.Set("text", text)
	data.Set("parse_mode", "Markdown")
	data.Set("disable_web_page_preview", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status %d", resp.StatusCode)
	}

	n.logger.Debug("сообщение отправлено в Telegram")
	return nil
}

// webhook отправляет JSON в webhook URL.
func (n *Notifier) webhook(ctx context.Context, meta *detector.Metadata) error {
	body := map[string]any{
		"event":        "new_jetton_minter",
		"name":         meta.Name,
		"symbol":       meta.Symbol,
		"address":      meta.Address,
		"code_hash":    meta.CodeHash,
		"minter_type":  meta.MinterType,
		"decimals":     meta.Decimals,
		"total_supply": meta.TotalSupply,
		"timestamp":    meta.Timestamp.Format(time.RFC3339),
		"links": map[string]string{
			"tonviewer":   tonViewerBase + meta.Address,
			"tonscan":     tonscanBase + meta.Address,
			"dexscreener": dexScreenerURL + meta.Address,
		},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}

	n.logger.Debug("webhook отправлен")
	return nil
}

// escapeMarkdown экранирует специальные символы для Telegram Markdown.
func escapeMarkdown(s string) string {
	replacer := []string{
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	}

	result := s
	for i := 0; i < len(replacer); i += 2 {
		result = replaceAll(result, replacer[i], replacer[i+1])
	}
	return result
}

func replaceAll(s, old, new string) string {
	for {
		idx := indexString(s, old)
		if idx == -1 {
			break
		}
		s = s[:idx] + new + s[idx+len(old):]
	}
	return s
}

func indexString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
