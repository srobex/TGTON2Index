# План улучшений HyperSniper-Indexer

На основе экспертного ревью. Цель: **честные 1-2 секунды** от включения транзакции в блок до сигнала торговому боту.

---

## 🔴 КРИТИЧЕСКИЕ (делать первыми)

### 1. StickyContext для консистентности
**Проблема:** Разные liteserver'ы могут быть на разных высотах блокчейна.
**Решение:** Использовать `client.StickyContext(ctx)` из tonutils-go для обработки одного блока.

```go
// Было:
master, err := c.api.CurrentMasterchainInfo(ctx)

// Надо:
stickyCtx := c.api.StickyContext(ctx)
master, err := c.api.CurrentMasterchainInfo(stickyCtx)
// Все последующие запросы для этого блока — через stickyCtx
```

**Файл:** `pkg/ton/client.go`
**Приоритет:** ⭐⭐⭐⭐⭐

---

### 2. Улучшить детектирование деплоя
**Проблема:** Сейчас просто проверяем `StateInit != nil`, можем ловить лишнее.
**Решение:** Проверять переход статуса аккаунта:

```go
// Правильное определение деплоя:
// old_status ∈ {nonexist, uninit}
// new_status = active
// StateInit != nil
```

**Файл:** `pkg/ton/client.go` → функция `isContractDeploy()`
**Приоритет:** ⭐⭐⭐⭐⭐

---

### 3. Интерфейсная проверка Jetton Minter (НЕ ТОЛЬКО code_hash!)
**Проблема:** Нет "официального" code_hash для TEP-74. Каждый может собрать свой контракт.
**Решение:** Проверять наличие get-методов:

```go
// После обнаружения деплоя вызываем:
// 1. get_jetton_data() - должен вернуть (total_supply, mintable, admin, content, wallet_code)
// 2. get_wallet_address(owner) - должен вернуть адрес Jetton Wallet

// Если оба метода работают и возвращают данные в правильном формате →
// это Jetton Minter с высокой вероятностью
```

**Файл:** `internal/detector/detector.go`
**Приоритет:** ⭐⭐⭐⭐⭐

---

### 4. Измерение и логирование latency
**Проблема:** Не знаем реальную задержку.
**Решение:** Добавить метрики:

```go
type LatencyMetrics struct {
    BlockUnixtime    int64   // время блока
    IndexerUnixtime  int64   // время обработки индексером  
    LatencyMs        int64   // разница в мс
}

// В каждом событии:
latencyMs := time.Now().UnixMilli() - blockTime.UnixMilli()
logger.Info("событие обработано", zap.Int64("latency_ms", latencyMs))
```

**Файлы:** `pkg/ton/client.go`, `internal/processor/processor.go`
**Приоритет:** ⭐⭐⭐⭐⭐

---

## 🟡 ВАЖНЫЕ УЛУЧШЕНИЯ

### 5. Расширить JSON payload для торгового бота
**Было:**
```json
{
  "address": "EQ...",
  "code_hash": "...",
  "name": "...",
  "symbol": "...",
  "timestamp": "..."
}
```

**Надо:**
```json
{
  "event": "jetton_minter_deployed",
  "minter_address": "EQ...",
  "workchain": 0,
  "seqno": 12345678,
  "tx_hash": "ABCD0123...",
  "code_hash": "b61941...",
  
  "jetton": {
    "name": "TokenName",
    "symbol": "TKN",
    "decimals": 9,
    "total_supply": "1000000000000000000",
    "content_uri": "https://.../meta.json"
  },
  
  "admin": {
    "address": "EQAdmin...",
    "is_contract": true
  },
  
  "flags": {
    "mintable": true,
    "verified_by_interface": true,
    "known_code_hash": false
  },
  
  "meta": {
    "block_unixtime": 1733910000,
    "indexer_unixtime": 1733910001,
    "latency_ms": 950
  }
}
```

**Файлы:** `internal/detector/detector.go` (Metadata struct), `internal/notifier/notifier.go`
**Приоритет:** ⭐⭐⭐⭐

---

### 6. Автоматический сбор code_hash
**Решение:** При успешной верификации по интерфейсу — сохранять code_hash в Redis/PG.

```go
// После подтверждения контракта как Jetton Minter:
if verifiedByInterface {
    detector.AddCodeHash(codeHash, "auto_verified_" + time.Now().Format("2006-01-02"))
    cache.RememberCodeHash(ctx, codeHash)
}
```

**Файлы:** `internal/detector/detector.go`, `internal/storage/redis_cache.go`
**Приоритет:** ⭐⭐⭐⭐

---

### 7. Увеличить worker pool
**Было:** `shardWorkers = 8`
**Надо:** `shardWorkers = 32` или `runtime.GOMAXPROCS(0) * 4`

На сервере с 64GB RAM и Ryzen это вообще не проблема.

**Файл:** `pkg/ton/client.go`
**Приоритет:** ⭐⭐⭐

---

### 8. Prometheus метрики
**Метрики для мониторинга:**

```go
// Latency
ton_block_to_indexer_ms           // histogram
poll_interval_actual_ms           // gauge

// Синхронизация
masterchain_lag_seqno             // gauge
masterchain_lag_seconds           // gauge

// Производительность  
blocks_processed_total            // counter
transactions_processed_total      // counter
deploys_detected_total            // counter
jetton_minters_detected_total     // counter

// По типам
jetton_minters_by_code_hash       // counter с label code_hash
jetton_minters_verified_by_interface  // counter

// Ошибки
liteserver_errors_total           // counter
get_method_timeouts_total         // counter
```

**Новый файл:** `internal/metrics/metrics.go`
**Приоритет:** ⭐⭐⭐

---

## 🟢 ДЛЯ ПРОДАКШЕНА

### 9. Подготовка к своему liteserver
**Зачем:** Публичные liteserver'ы имеют задержки и rate-limits.
**План:**
1. Изучить деплой ton-node + liteserver
2. Добавить в конфиг опцию `custom_liteserver_config`
3. Разместить node в том же датацентре, что и индексер

**Приоритет:** ⭐⭐⭐ (после MVP)

---

### 10. PostgreSQL для персистентности
**Таблицы:**

```sql
CREATE TABLE jetton_minters_detected (
    id SERIAL PRIMARY KEY,
    minter_address VARCHAR(68) UNIQUE,
    code_hash VARCHAR(64),
    admin_address VARCHAR(68),
    name VARCHAR(255),
    symbol VARCHAR(32),
    decimals INT,
    content_uri TEXT,
    tx_hash VARCHAR(64),
    seqno BIGINT,
    block_unixtime BIGINT,
    detected_at TIMESTAMP DEFAULT NOW(),
    latency_ms INT,
    verified_by_interface BOOLEAN,
    trust_level VARCHAR(32) -- 'core', 'dex', 'auto_verified', 'unknown'
);

CREATE TABLE known_code_hashes (
    code_hash VARCHAR(64) PRIMARY KEY,
    description VARCHAR(255),
    trust_level VARCHAR(32),
    source VARCHAR(255), -- 'manual', 'auto', 'tonapi', 'dex'
    added_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE events_sent (
    id SERIAL PRIMARY KEY,
    minter_address VARCHAR(68),
    channel VARCHAR(32), -- 'telegram', 'webhook'
    status VARCHAR(32),
    sent_at TIMESTAMP DEFAULT NOW(),
    response_code INT
);
```

**Приоритет:** ⭐⭐ (после стабилизации)

---

### 11. Redis Pub/Sub вместо Webhook (для локальной связи)
**Если бот на том же сервере:**

```go
// Индексер публикует:
redisClient.Publish(ctx, "jetton_minters", jsonPayload)

// Бот подписывается:
pubsub := redisClient.Subscribe(ctx, "jetton_minters")
for msg := range pubsub.Channel() {
    // обработка события
}
```

**Плюсы:** Меньше latency, буферизация, можно несколько подписчиков.

**Приоритет:** ⭐⭐

---

### 12. Базовый риск-скоринг
**На уровне индексера можно добавить:**

```go
type RiskScore struct {
    Score       int    // 0-100
    Reasons     []string
}

func calculateRiskScore(meta *Metadata) RiskScore {
    score := 50 // базовый
    reasons := []string{}
    
    // Плюсы
    if meta.VerifiedByInterface {
        score += 20
        reasons = append(reasons, "verified_interface")
    }
    if meta.KnownCodeHash {
        score += 30
        reasons = append(reasons, "known_code_hash")
    }
    if meta.ContentURI != "" {
        score += 10
        reasons = append(reasons, "has_metadata")
    }
    
    // Минусы
    if meta.AdminIsContract {
        score -= 10
        reasons = append(reasons, "admin_is_contract")
    }
    if meta.Decimals == 0 {
        score -= 20
        reasons = append(reasons, "zero_decimals")
    }
    
    return RiskScore{Score: score, Reasons: reasons}
}
```

**Важно:** Полноценный антискам — задача торгового бота (проверка ликвидности, tax, и т.д.)

**Приоритет:** ⭐⭐

---

## 📋 Порядок выполнения

### Фаза 1: Критические улучшения (1-2 дня)
- [ ] 1. StickyContext
- [ ] 2. Улучшить isContractDeploy()
- [ ] 3. Интерфейсная проверка get_jetton_data/get_wallet_address
- [ ] 4. Измерение latency

### Фаза 2: Важные улучшения (2-3 дня)
- [ ] 5. Расширить JSON payload
- [ ] 6. Автоматический сбор code_hash
- [ ] 7. Увеличить worker pool
- [ ] 8. Prometheus метрики

### Фаза 3: Продакшен (по мере необходимости)
- [ ] 9. Свой liteserver
- [ ] 10. PostgreSQL
- [ ] 11. Redis Pub/Sub
- [ ] 12. Риск-скоринг

---

## 🔗 Полезные ссылки

- [tonutils-go README](https://github.com/xssnick/tonutils-go) — про StickyContext
- [TEP-74 Jetton Standard](https://github.com/ton-blockchain/TEPs/blob/master/text/0074-jettons-standard.md)
- [TEP-176/177 Mintless Jettons](https://docs.ton.org/develop/dapps/asset-processing/mintless-jettons)
- [toncenter/ton-indexer](https://github.com/toncenter/ton-indexer) — референс
- [anton indexer](https://anton.tools) — ещё один референс
- [Ston.fi Pool API](https://docs.ston.fi) — для получения code_hash DEX токенов

---

## ⚠️ Важные заметки

1. **code_hash НЕ фиксирован для TEP-74** — это интерфейс, а не конкретный байткод
2. **Интерфейсная проверка обязательна** — иначе пропустим новые реализации
3. **StickyContext критичен** — без него можем получать несогласованные данные
4. **Latency измерять с первого дня** — иначе не поймём, достигли ли цели
5. **Свой liteserver** — для боевого режима почти обязателен

---

*Создано: 2024-12-11*
*На основе экспертного ревью*

