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
	tonViewerBase = "https://tonviewer.com/"
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
	n.console(meta)

	if n.tgToken != "" && n.tgChatID != "" {
		if err := n.telegram(ctx, meta); err != nil {
			n.logger.Warn("ошибка отправки в Telegram", zap.Error(err))
		}
	}

	if n.webhookURL != "" {
		if err := n.webhook(ctx, meta); err != nil {
			n.logger.Warn("ошибка отправки в webhook", zap.Error(err))
		}
	}
}

func (n *Notifier) console(meta *detector.Metadata) {
	msg := fmt.Sprintf("JETTON MINTER: %s (%s) [%s]", meta.Name, meta.Symbol, meta.Address)
	link := tonViewerBase + meta.Address
	color.New(color.FgHiGreen, color.Bold).Printf("%s\n", msg)
	color.New(color.FgCyan).Printf("Code hash: %s | Decimals: %d | Link: %s\n", meta.CodeHash, meta.Decimals, link)
}

func (n *Notifier) telegram(ctx context.Context, meta *detector.Metadata) error {
	text := fmt.Sprintf(
		"🚀 Новый JettonMinter\nНазвание: %s\nТикер: %s\nАдрес: %s\nCodeHash: %s\nTonviewer: %s%s\nВремя: %s",
		meta.Name, meta.Symbol, meta.Address, meta.CodeHash, tonViewerBase, meta.Address, meta.Timestamp.Format(time.RFC3339),
	)

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.tgToken)
	data := url.Values{}
	data.Set("chat_id", n.tgChatID)
	data.Set("text", text)
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
	return nil
}

func (n *Notifier) webhook(ctx context.Context, meta *detector.Metadata) error {
	body := map[string]any{
		"name":      meta.Name,
		"symbol":    meta.Symbol,
		"address":   meta.Address,
		"code_hash": meta.CodeHash,
		"decimals":  meta.Decimals,
		"timestamp": meta.Timestamp.Format(time.RFC3339),
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
	return nil
}

