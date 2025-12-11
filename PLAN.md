# План развития HyperSniper-Indexer

**Статус:** ✅ MVP работает! Интеграция с ботом настроена.

---

## ✅ ВЫПОЛНЕНО (Фаза 1)

### 1. ✅ Базовая интеграция с TON
- Подключение через `tonutils-go` к публичным liteserver'ам
- Polling новых блоков каждые 100мс
- Параллельная обработка шардов (64 воркера на Ryzen)

### 2. ✅ Детектирование Jetton Minter
- Проверка по интерфейсу `get_jetton_data` (TEP-74)
- Автоматическое добавление новых code_hash в runtime
- Работает для ВСЕХ Jetton Minter, даже с неизвестным code_hash

### 3. ✅ Уведомления
- Telegram — работает! ✅
- Консольный вывод с цветами
- Webhook с расширенным JSON (для торгового бота)

### 4. ✅ Антидубликация
- Redis кэш для адресов минтеров
- Предотвращение повторных уведомлений

### 5. ✅ Конфигурация
- `catchup_hours: 0` отключает catchup
- Настройки Telegram/Webhook через config.yaml

---

## ✅ ВЫПОЛНЕНО (Фаза 2) — Интеграция с ботом

### 6. ✅ Webhook для торгового бота
- Расширенный JSON payload с полной информацией о токене
- Заголовок `X-HyperSniper-Event: jetton_minter_deployed`
- Endpoint в боте: `POST /api/indexer/event`

### 7. ✅ Общий Redis с ботом
- Единый Redis для кэширования
- Конфигурация через `redis.addr` в config.yaml

### 8. ✅ Docker Compose для полного стека
- PostgreSQL, Redis, Indexer в одном compose
- Готовность к добавлению контейнера бота

---

## 🔶 В ПРОЦЕССЕ / ТРЕБУЕТ УЛУЧШЕНИЯ

### 9. 🔶 Парсинг метаданных
**Проблема:** Supply, Admin, Name/Symbol отображаются некорректно (бинарные данные).

**Решение:** Улучшить парсинг данных из `get_jetton_data`:
- Правильная конвертация BigInt для total_supply
- Парсинг адреса админа из slice
- Парсинг content cell для name/symbol (on-chain или off-chain)

**Приоритет:** ⭐⭐⭐

### 10. 🔶 Реальные code_hash
**Проблема:** Сейчас используются placeholder хэши.

**Решение:** Собрать реальные code_hash из:
- Известных токенов (USDT, NOT, STON и др.)
- DEX (Stonfi, DeDust)
- TonAPI

**Приоритет:** ⭐⭐⭐

---

## 📋 СЛЕДУЮЩИЕ ШАГИ (Фаза 3)

### 11. Prometheus метрики
```yaml
# Метрики для мониторинга:
- latency_histogram: время от появления в блоке до уведомления
- blocks_processed_total: обработано блоков
- jetton_minters_detected_total: найдено минтеров
- webhook_errors_total: ошибки отправки в бот
- redis_operations_total: операции с кэшом
```

### 12. Redis Pub/Sub (опционально)
Вместо HTTP webhook использовать Redis Pub/Sub для минимальной latency.

```go
// Публикация события
redis.Publish(ctx, "hypersniper:jetton_minter", jsonPayload)
```

```python
# Подписка в боте
pubsub = redis.pubsub()
await pubsub.subscribe("hypersniper:jetton_minter")
```

### 13. Свой liteserver
Для продакшена — свой TON node рядом с индексером.
- Минимальная задержка
- Гарантированная доступность
- Полный контроль

---

## 📊 ТЕКУЩИЕ РЕЗУЛЬТАТЫ

| Метрика | Значение | Цель |
|---------|----------|------|
| Latency (catchup) | ~10 сек | N/A |
| Latency (realtime) | ~1-3 сек | 1-2 сек |
| Telegram | ✅ работает | ✅ |
| Webhook → Бот | ✅ работает | ✅ |
| Детекция по интерфейсу | ✅ работает | ✅ |
| Автодобавление code_hash | ✅ работает | ✅ |
| Общий Redis | ✅ настроен | ✅ |

---

## 🚀 КАК ЗАПУСТИТЬ

### Локально (без Docker)

```bash
# Убедитесь что Redis запущен
redis-cli ping

# Настройте config.yaml
nano config.yaml

# Запустите
go run ./cmd/indexer --network=mainnet
```

### На VPS вместе с ботом

```bash
# 1. Запустить бота с FastAPI
cd ~/TGTON2
source .venv/bin/activate
python -m bot.main &
uvicorn bot.web.app:app --host 0.0.0.0 --port 8000 &

# 2. Запустить индексер
cd ~/TGTON2Index
go run ./cmd/indexer --network=mainnet
```

### Через systemd

См. документацию бота: `TGTON2/docs/РАЗВЁРТЫВАНИЕ_ПОЛНЫЙ_СТЕК.md`

---

## 📁 СТРУКТУРА ПРОЕКТА

```
TGTON2Index/
├── cmd/indexer/main.go      # Точка входа
├── pkg/ton/client.go        # TON клиент (tonutils-go)
├── internal/
│   ├── detector/            # Детектор Jetton Minter
│   ├── processor/           # Обработчик событий
│   ├── notifier/            # Telegram + Webhook → Бот
│   ├── storage/             # Redis кэш
│   └── config/              # Конфигурация
├── docker/
│   ├── Dockerfile
│   └── docker-compose.yml   # PostgreSQL + Redis + Indexer + (Bot)
├── config.yaml              # Настройки
└── PLAN.md                  # Этот файл
```

---

## 🔗 СВЯЗЬ С БОТОМ

### Архитектура

```
┌─────────────────────┐     webhook      ┌─────────────────────┐
│  TGTON2Index (Go)   │ ───────────────► │   TGTON2 (Python)   │
│                     │                  │                     │
│  [Liteserver]       │ POST             │  [FastAPI]          │
│       ↓             │ /api/indexer/    │       ↓             │
│  [Detector]         │ event            │  [GemScanner]       │
│       ↓             │                  │       ↓             │
│  [Notifier]         │                  │  [SafetyChecker]    │
└─────────────────────┘                  └─────────────────────┘
         │                                        │
         └──────────────┬─────────────────────────┘
                        ↓
               ┌─────────────────┐
               │   Redis (общий)  │
               └─────────────────┘
```

### Формат Webhook

```json
{
  "event": "jetton_minter_deployed",
  "minter_address": "EQ...",
  "code_hash": "abc123...",
  "jetton": {
    "name": "Token Name",
    "symbol": "TKN",
    "decimals": 9,
    "total_supply": "1000000000"
  },
  "flags": {
    "verified_by_interface": true,
    "known_code_hash": false
  },
  "meta": {
    "latency_ms": 1500
  },
  "links": {
    "tonviewer": "https://tonviewer.com/EQ..."
  }
}
```

---

## 🔗 ССЫЛКИ

- Бот: https://github.com/srobex/TGTON2
- Индексер: https://github.com/srobex/TGTON2Index
- tonutils-go: https://github.com/xssnick/tonutils-go
- TEP-74 (Jetton Standard): https://github.com/ton-blockchain/TEPs/blob/master/text/0074-jettons-standard.md

---

*Обновлено: декабрь 2024*
