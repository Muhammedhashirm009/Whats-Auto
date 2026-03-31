# WhatsBridge — API Integration Guide

> **Version**: 1.0  
> **Base URL**: `https://your-whatsbridge-instance.koyeb.app` (or `http://localhost:8000` for local dev)

---

## Quick Start

WhatsBridge exposes a simple REST API for sending WhatsApp messages from any application. The integration takes **3 steps**:

1. Generate an API key from the dashboard (`/api-keys`)
2. Set `Authorization: Bearer <your-key>` on every request
3. `POST /api/send` with a phone number and message

---

## Authentication

All API endpoints require a **Bearer token** in the `Authorization` header.

```
Authorization: Bearer wb_a1b2c3d4e5f6...
```

> **Note**: If no API keys have been created yet, the API runs in **open mode** (no auth required). Once you generate your first key, all endpoints are protected.

Generate keys from the WhatsBridge dashboard at `/api-keys`.

---

## Phone Number Format

Phone numbers must be in **international format without `+` or spaces**:

| Input | Formatted |
|-------|-----------|
| `+91 98765 43210` | `919876543210` |
| `9876543210` (10-digit India) | `919876543210` |
| `15551234567` (US) | `15551234567` |

Strip all non-digits and prepend the country code if missing.

---

## Endpoints

### 1. Check Bot Status

Verify the WhatsApp bot is connected before sending.

```
GET /api/status
```

**Response:**
```json
{
  "connected": true,
  "loggedIn": true
}
```

| Field | Meaning |
|-------|---------|
| `connected` | Socket connection to WhatsApp servers is active |
| `loggedIn` | A WhatsApp account is authenticated (QR scanned) |

> ✅ **Ready to send** = both `connected` AND `loggedIn` are `true`

---

### 2. Send a Text Message

```
POST /api/send
Content-Type: application/json
```

**Request Body:**
```json
{
  "to": "919876543210",
  "message": "Hello from WhatsBridge!"
}
```

**Success Response** (`200`):
```json
{
  "success": true
}
```

**Error Response** (`503` if bot offline, `400` if missing fields, `500` if send fails):
```json
{
  "success": false,
  "error": "Bot is not connected to WhatsApp"
}
```

---

### 3. Send a File (Image, PDF, Video, Audio)

Use **multipart/form-data** to upload a file along with a caption.

```
POST /api/send
Content-Type: multipart/form-data
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `to` | string | Yes | Phone number |
| `message` | string | No | Caption text (optional for files) |
| `file` | file | Yes | The file to send (PDF, image, video, etc.) |

**File type handling:**

| Detected Type | WhatsApp Display |
|---------------|-----------------|
| `image/*` | Image with caption |
| `video/*` | Video with caption |
| `audio/*` | Audio message |
| Everything else | Document attachment |

> **Max file size**: 50 MB

---

### 4. Bulk Send

Dispatch multiple messages in the background with a configurable delay between sends.

```
POST /api/bulk-send
Content-Type: application/json
```

**Request Body:**
```json
{
  "messages": [
    { "to": "919876543210", "message": "Hi Customer 1!" },
    { "to": "918765432109", "message": "Hi Customer 2!" },
    { "to": "917654321098", "message": "Hi Customer 3!" }
  ],
  "interval_ms": 2000
}
```

| Field | Type | Description |
|-------|------|-------------|
| `messages` | array | Array of `{to, message}` objects |
| `interval_ms` | int | Delay between sends in milliseconds (prevents rate-limiting) |

**Response** (`200`):
```json
{
  "success": true,
  "message": "Started dispatching 3 messages"
}
```

> ⚠️ Messages are dispatched in the background. The response confirms the dispatch was **started**, not that all messages were delivered.

---

### 5. Schedule a Message

Queue a message for future delivery.

```
POST /api/schedule
Content-Type: application/json
```

**Request Body:**
```json
{
  "to": "919876543210",
  "message": "Reminder: Your appointment is tomorrow at 10 AM",
  "scheduled_for": "2026-04-01T10:00:00Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `to` | string | Phone number |
| `message` | string | Message text |
| `scheduled_for` | string | ISO 8601 / RFC 3339 datetime (UTC recommended) |

**Response** (`200`):
```json
{
  "success": true
}
```

> The scheduler polls every 10 seconds and sends pending messages automatically.

---

### 6. Get Usage Metrics

```
GET /api/metrics
```

**Response:**
```json
{
  "total_sent": 1250,
  "total_failed": 12,
  "scheduled_count": 3
}
```

---

### 7. Connection Management

```
POST /api/connect     → Reconnect the bot
POST /api/disconnect  → Disconnect the bot  
POST /api/logout      → Wipe session and show new QR code
GET  /api/qr          → Get current QR code string
```

---

## Integration Examples

### cURL

```bash
# Check status
curl -H "Authorization: Bearer wb_YOUR_KEY" \
  https://your-app.koyeb.app/api/status

# Send text
curl -X POST \
  -H "Authorization: Bearer wb_YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"to":"919876543210","message":"Hello!"}' \
  https://your-app.koyeb.app/api/send

# Send file
curl -X POST \
  -H "Authorization: Bearer wb_YOUR_KEY" \
  -F "to=919876543210" \
  -F "message=Your invoice" \
  -F "file=@/path/to/invoice.pdf" \
  https://your-app.koyeb.app/api/send
```

---

### PHP (Laravel)

**1. Add to `.env`:**
```env
WHATSBRIDGE_URL=https://your-app.koyeb.app
WHATSBRIDGE_API_KEY=wb_YOUR_KEY
```

**2. Add to `config/services.php`:**
```php
'whatsbridge' => [
    'url' => env('WHATSBRIDGE_URL', 'http://localhost:8000'),
    'api_key' => env('WHATSBRIDGE_API_KEY', ''),
],
```

**3. Create `app/Services/WhatsBridgeService.php`:**
```php
<?php

namespace App\Services;

use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Log;

class WhatsBridgeService
{
    protected string $baseUrl;
    protected string $apiKey;

    public function __construct()
    {
        $this->baseUrl = rtrim(config('services.whatsbridge.url'), '/');
        $this->apiKey  = config('services.whatsbridge.api_key', '');
    }

    /**
     * Build an HTTP request with auth header.
     */
    protected function request()
    {
        $req = Http::timeout(15);
        if ($this->apiKey) {
            $req = $req->withHeaders([
                'Authorization' => 'Bearer ' . $this->apiKey,
            ]);
        }
        return $req;
    }

    /**
     * Check if the bot is online and ready.
     */
    public function isOnline(): bool
    {
        try {
            $response = $this->request()->get("{$this->baseUrl}/api/status");
            $data = $response->json();
            return !empty($data['connected']) && !empty($data['loggedIn']);
        } catch (\Exception $e) {
            return false;
        }
    }

    /**
     * Send a text message.
     */
    public function sendText(string $phone, string $message): bool
    {
        try {
            $response = $this->request()->post("{$this->baseUrl}/api/send", [
                'to'      => $this->formatPhone($phone),
                'message' => $message,
            ]);
            return $response->successful() && !empty($response->json('success'));
        } catch (\Exception $e) {
            Log::error("WhatsBridge send failed: {$e->getMessage()}");
            return false;
        }
    }

    /**
     * Send a file (PDF, image, etc.) with an optional caption.
     */
    public function sendFile(string $phone, string $filePath, string $caption = ''): bool
    {
        if (!file_exists($filePath)) {
            Log::error("WhatsBridge: file not found: {$filePath}");
            return false;
        }

        try {
            $response = $this->request()
                ->attach('file', file_get_contents($filePath), basename($filePath))
                ->post("{$this->baseUrl}/api/send", [
                    'to'      => $this->formatPhone($phone),
                    'message' => $caption,
                ]);
            return $response->successful() && !empty($response->json('success'));
        } catch (\Exception $e) {
            Log::error("WhatsBridge file send failed: {$e->getMessage()}");
            return false;
        }
    }

    /**
     * Schedule a message for future delivery.
     */
    public function schedule(string $phone, string $message, string $dateTimeUtc): bool
    {
        try {
            $response = $this->request()->post("{$this->baseUrl}/api/schedule", [
                'to'            => $this->formatPhone($phone),
                'message'       => $message,
                'scheduled_for' => $dateTimeUtc,
            ]);
            return $response->successful() && !empty($response->json('success'));
        } catch (\Exception $e) {
            Log::error("WhatsBridge schedule failed: {$e->getMessage()}");
            return false;
        }
    }

    /**
     * Format phone number to international format (strips +, spaces).
     */
    protected function formatPhone(string $phone): string
    {
        $phone = preg_replace('/\D/', '', $phone);

        // If 10 digits (India), prepend country code
        if (strlen($phone) === 10) {
            return '91' . $phone;
        }

        return $phone;
    }
}
```

**4. Usage in any Controller or Job:**
```php
use App\Services\WhatsBridgeService;

class OrderController extends Controller
{
    public function confirmOrder(Request $request, WhatsBridgeService $wa)
    {
        // ... save order ...

        // Send text notification
        $wa->sendText(
            $order->customer_phone,
            "Hi {$order->customer_name}, your order #{$order->id} has been confirmed!"
        );

        // Send invoice PDF
        $wa->sendFile(
            $order->customer_phone,
            storage_path("invoices/invoice_{$order->id}.pdf"),
            "Invoice for Order #{$order->id}"
        );

        return redirect()->back()->with('success', 'Order confirmed!');
    }
}
```

---

### Python

```python
import requests

BASE_URL = "https://your-app.koyeb.app"
API_KEY  = "wb_YOUR_KEY"
HEADERS  = {"Authorization": f"Bearer {API_KEY}"}

# Check status
status = requests.get(f"{BASE_URL}/api/status", headers=HEADERS).json()
print(f"Bot online: {status['connected'] and status['loggedIn']}")

# Send text
resp = requests.post(f"{BASE_URL}/api/send", headers=HEADERS, json={
    "to": "919876543210",
    "message": "Hello from Python!"
})
print(resp.json())

# Send file
with open("invoice.pdf", "rb") as f:
    resp = requests.post(
        f"{BASE_URL}/api/send",
        headers=HEADERS,
        data={"to": "919876543210", "message": "Your invoice"},
        files={"file": ("invoice.pdf", f, "application/pdf")}
    )
print(resp.json())
```

---

### Node.js

```javascript
const BASE_URL = "https://your-app.koyeb.app";
const API_KEY  = "wb_YOUR_KEY";

// Send text
const res = await fetch(`${BASE_URL}/api/send`, {
  method: "POST",
  headers: {
    "Authorization": `Bearer ${API_KEY}`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    to: "919876543210",
    message: "Hello from Node.js!"
  }),
});

const data = await res.json();
console.log(data); // { success: true }
```

---

### .NET (C#)

```csharp
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;

var client = new HttpClient();
client.BaseAddress = new Uri("https://your-app.koyeb.app");
client.DefaultRequestHeaders.Authorization =
    new AuthenticationHeaderValue("Bearer", "wb_YOUR_KEY");

// Send text
var payload = JsonSerializer.Serialize(new {
    to = "919876543210",
    message = "Hello from C#!"
});

var response = await client.PostAsync("/api/send",
    new StringContent(payload, Encoding.UTF8, "application/json"));

var result = await response.Content.ReadAsStringAsync();
Console.WriteLine(result); // {"success":true}
```

---

## Error Codes

| HTTP Status | Meaning |
|-------------|---------|
| `200` | Success |
| `400` | Bad Request — missing `to`, `message`, or invalid format |
| `401` | Unauthorized — missing or invalid API key |
| `403` | Forbidden — API key is inactive |
| `405` | Method Not Allowed — wrong HTTP method |
| `500` | Internal Error — message send failed |
| `503` | Service Unavailable — bot is not connected to WhatsApp |

---

## WebSocket Bridge (Advanced)

For real-time relay (e.g., from a persistent server), connect via WebSocket:

```
wss://your-app.koyeb.app/ws/bridge
```

Send JSON frames:

```json
// Send text
{ "action": "SEND_MESSAGE", "payload": { "phone": "919876543210", "message": "Hello!" } }

// Send document (download from URL)
{ "action": "SEND_DOCUMENT", "payload": { "phone": "919876543210", "document_url": "https://example.com/file.pdf", "filename": "invoice.pdf", "caption": "Your invoice" } }
```

> **Note**: The WebSocket bridge does not require an API key. Use it for internal/trusted connections only.

---

## Best Practices

1. **Always check `/api/status` before sending** — avoids wasted requests when the bot is offline
2. **Use `interval_ms` in bulk sends** — WhatsApp may rate-limit rapid messages; 2000ms+ is safe
3. **Store your API key in environment variables** — never hardcode it
4. **Format phone numbers server-side** — strip all non-digits, prepend country code
5. **Handle errors gracefully** — the bot may disconnect temporarily; implement retry logic
6. **Use multipart/form-data for files** — JSON body only supports text messages
