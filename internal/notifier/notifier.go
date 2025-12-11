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
	"github.com/yourname/hyper-sniper-indexer/pkg/ton"
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

// Notify отправляет уведомление (обратная совместимость).
func (n *Notifier) Notify(ctx context.Context, meta *detector.Metadata) {
	n.NotifyWithEvent(ctx, meta, nil)
}

// NotifyWithEvent отправляет уведомление с полными данными события.
func (n *Notifier) NotifyWithEvent(ctx context.Context, meta *detector.Metadata, event *ton.Event) {
	// Консольный вывод (всегда)
	n.console(meta)

	// Telegram (если настроен)
	if n.tgToken != "" && n.tgChatID != "" {
		if err := n.telegram(ctx, meta); err != nil {
			n.logger.Warn("ошибка отправки в Telegram", zap.Error(err))
		}
	}

	// Webhook (если настроен) — расширенный JSON для торгового бота
	if n.webhookURL != "" {
		if err := n.webhookExtended(ctx, meta, event); err != nil {
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
	red := color.New(color.FgRed)

	fmt.Println()
	green.Println("╔══════════════════════════════════════════════════════════════╗")
	green.Println("║           🚀 НОВЫЙ JETTON MINTER ОБНАРУЖЕН! 🚀              ║")
	green.Println("╚══════════════════════════════════════════════════════════════╝")

	if meta.Name != "" || meta.Symbol != "" {
		yellow.Printf("  Название: %s (%s)\n", meta.Name, meta.Symbol)
	}

	white.Printf("  Адрес:    %s\n", meta.Address)
	cyan.Printf("  Тип:      %s\n", meta.MinterType)

	// Статус верификации
	if meta.VerifiedByInterface && meta.KnownCodeHash {
		green.Printf("  Статус:   ✅ Верифицирован (известный code + интерфейс)\n")
	} else if meta.VerifiedByInterface {
		yellow.Printf("  Статус:   ⚠️ Новый тип (верифицирован по интерфейсу)\n")
	} else if meta.KnownCodeHash {
		cyan.Printf("  Статус:   ✓ Известный code_hash\n")
	} else {
		red.Printf("  Статус:   ❓ Неизвестный\n")
	}

	white.Printf("  CodeHash: %s\n", truncateHash(meta.CodeHash))

	if meta.TotalSupply != "" {
		white.Printf("  Supply:   %s\n", meta.TotalSupply)
	}
	if meta.AdminAddr != "" {
		white.Printf("  Admin:    %s\n", truncateHash(meta.AdminAddr))
	}
	if meta.Mintable {
		white.Printf("  Mintable: да\n")
	}

	fmt.Println()
	cyan.Printf("  📎 Tonviewer:    %s%s\n", tonViewerBase, meta.Address)
	cyan.Printf("  📎 Tonscan:      %s%s\n", tonscanBase, meta.Address)
	cyan.Printf("  📎 DexScreener:  %s%s\n", dexScreenerURL, meta.Address)

	// Latency
	yellow.Printf("\n  ⚡ Latency: %d ms\n", meta.DetectionLatencyMs)
	white.Printf("  ⏱️  Время:   %s\n", meta.Timestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Println()
}

// telegram отправляет сообщение в Telegram.
func (n *Notifier) telegram(ctx context.Context, meta *detector.Metadata) error {
	// Формируем статус
	var status string
	if meta.VerifiedByInterface && meta.KnownCodeHash {
		status = "✅ Верифицирован"
	} else if meta.VerifiedByInterface {
		status = "⚠️ Новый тип (interface OK)"
	} else if meta.KnownCodeHash {
		status = "✓ Известный code"
	} else {
		status = "❓ Неизвестный"
	}

	var text string
	if meta.Name != "" || meta.Symbol != "" {
		text = fmt.Sprintf(
			"🚀 *JETTON MINTER*\n\n"+
				"📝 *Название:* %s\n"+
				"🏷️ *Тикер:* %s\n"+
				"📍 *Адрес:* `%s`\n"+
				"🔧 *Тип:* %s\n"+
				"📊 *Статус:* %s\n"+
				"⚡ *Latency:* %d ms\n\n"+
				"🔍 [Tonviewer](%s%s) | [Tonscan](%s%s)\n\n"+
				"⏱️ %s",
			escapeMarkdown(meta.Name),
			escapeMarkdown(meta.Symbol),
			meta.Address,
			escapeMarkdown(meta.MinterType),
			status,
			meta.DetectionLatencyMs,
			tonViewerBase, meta.Address,
			tonscanBase, meta.Address,
			meta.Timestamp.Format("15:04:05 MST"),
		)
	} else {
		text = fmt.Sprintf(
			"🚀 *JETTON MINTER*\n\n"+
				"📍 *Адрес:* `%s`\n"+
				"🔧 *Тип:* %s\n"+
				"📊 *Статус:* %s\n"+
				"⚡ *Latency:* %d ms\n\n"+
				"🔍 [Tonviewer](%s%s) | [Tonscan](%s%s)\n\n"+
				"⏱️ %s",
			meta.Address,
			escapeMarkdown(meta.MinterType),
			status,
			meta.DetectionLatencyMs,
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

// WebhookPayload структура JSON для торгового бота (расширенная версия).
type WebhookPayload struct {
	Event         string `json:"event"`
	MinterAddress string `json:"minter_address"`
	Workchain     int32  `json:"workchain"`
	Seqno         uint32 `json:"seqno"`
	TxHash        string `json:"tx_hash,omitempty"`
	TxLT          uint64 `json:"tx_lt,omitempty"`
	CodeHash      string `json:"code_hash"`

	Jetton JettonInfo `json:"jetton"`
	Admin  AdminInfo  `json:"admin"`
	Flags  FlagsInfo  `json:"flags"`
	Meta   MetaInfo   `json:"meta"`
	Links  LinksInfo  `json:"links"`
}

type JettonInfo struct {
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	Decimals    int    `json:"decimals"`
	TotalSupply string `json:"total_supply"`
	ContentURI  string `json:"content_uri,omitempty"`
}

type AdminInfo struct {
	Address    string `json:"address"`
	IsContract bool   `json:"is_contract"`
}

type FlagsInfo struct {
	Mintable            bool `json:"mintable"`
	VerifiedByInterface bool `json:"verified_by_interface"`
	KnownCodeHash       bool `json:"known_code_hash"`
}

type MetaInfo struct {
	BlockUnixtime   int64  `json:"block_unixtime"`
	IndexerUnixtime int64  `json:"indexer_unixtime"`
	LatencyMs       int64  `json:"latency_ms"`
	MinterType      string `json:"minter_type"`
}

type LinksInfo struct {
	Tonviewer   string `json:"tonviewer"`
	Tonscan     string `json:"tonscan"`
	DexScreener string `json:"dexscreener"`
}

// webhookExtended отправляет расширенный JSON в webhook для торгового бота.
func (n *Notifier) webhookExtended(ctx context.Context, meta *detector.Metadata, event *ton.Event) error {
	payload := WebhookPayload{
		Event:         "jetton_minter_deployed",
		MinterAddress: meta.Address,
		CodeHash:      meta.CodeHash,

		Jetton: JettonInfo{
			Name:        meta.Name,
			Symbol:      meta.Symbol,
			Decimals:    meta.Decimals,
			TotalSupply: meta.TotalSupply,
			ContentURI:  meta.ContentURI,
		},

		Admin: AdminInfo{
			Address:    meta.AdminAddr,
			IsContract: len(meta.AdminAddr) > 0 && meta.AdminAddr[0] != 'E', // упрощённая проверка
		},

		Flags: FlagsInfo{
			Mintable:            meta.Mintable,
			VerifiedByInterface: meta.VerifiedByInterface,
			KnownCodeHash:       meta.KnownCodeHash,
		},

		Meta: MetaInfo{
			IndexerUnixtime: time.Now().Unix(),
			LatencyMs:       meta.DetectionLatencyMs,
			MinterType:      meta.MinterType,
		},

		Links: LinksInfo{
			Tonviewer:   tonViewerBase + meta.Address,
			Tonscan:     tonscanBase + meta.Address,
			DexScreener: dexScreenerURL + meta.Address,
		},
	}

	// Добавляем данные из события если есть
	if event != nil {
		payload.Workchain = event.Workchain
		payload.Seqno = event.Seqno
		payload.TxHash = event.TxHash
		payload.TxLT = event.TxLT
		payload.Meta.BlockUnixtime = event.BlockUnixtime
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HyperSniper-Event", "jetton_minter_deployed")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}

	n.logger.Debug("webhook отправлен", zap.Int("status", resp.StatusCode))
	return nil
}

// truncateHash обрезает хэш для отображения.
func truncateHash(hash string) string {
	if len(hash) <= 20 {
		return hash
	}
	return hash[:8] + "..." + hash[len(hash)-8:]
}

// escapeMarkdown экранирует специальные символы для Telegram Markdown.
func escapeMarkdown(s string) string {
	if s == "" {
		return "-"
	}

	// Простое экранирование основных символов
	result := s
	chars := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}

	for _, char := range chars {
		result = replaceAll(result, char, "\\"+char)
	}
	return result
}

func replaceAll(s, old, new string) string {
	for {
		idx := -1
		for i := 0; i <= len(s)-len(old); i++ {
			if s[i:i+len(old)] == old {
				idx = i
				break
			}
		}
		if idx == -1 {
			break
		}
		s = s[:idx] + new + s[idx+len(old):]
	}
	return s
}
