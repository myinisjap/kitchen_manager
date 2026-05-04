# Kitchen Manager

A self-hosted app for tracking your pantry, planning meals, managing recipes, and generating shopping lists. Runs as a single program — no cloud account required.

## Screenshots

<details>
<summary>Pantry</summary>

![Pantry](example_screenshots/pantry.png)

</details>
<details>
<summary>Shopping List</summary>

![Shopping](example_screenshots/shopping.png)

</details>
<details>
<summary>Recipes</summary>

![Recipes](example_screenshots/recipes.png)

</details>
<details>
<summary>Meal Calendar</summary>

![Calendar](example_screenshots/calendar.png)

</details>
<details>
<summary>Meal History</summary>

![History](example_screenshots/history.png)

</details>

## Features

- **Pantry tracking** — quantities, units, locations, expiry dates, low-stock alerts
- **Barcode scanning** — use your phone camera to look up or add items
- **Shopping list** — manual items, auto-generated from low-stock thresholds, or pulled from recipes
- **Recipe import** — paste a URL from any major recipe site; ingredients auto-link to your pantry
- **Meal calendar** — plan breakfast/lunch/dinner for the week; cooking a meal deducts ingredients
- **Meal history & cost tracking** — log meals cooked and see weekly/monthly spend
- **Dark mode** — toggle in the header
- **Data backup** — download your database any time from the 💾 button in the header

---

## Quick Start (Docker — recommended)

You'll need [Docker](https://docs.docker.com/get-docker/) installed.

### 1. Download the files

```bash
git clone https://github.com/jpierce42/kitchen_manager
cd kitchen_manager
```

Or download and unzip the source from the Releases page.

### 2. Create your config file

```bash
cp .env.example .env
```

Open `.env` in a text editor. For a basic setup with no login required, you only need to set:

```
DB_PATH=/data/kitchen.db
```

Leave everything else blank for now. You can add Google login later.

### 3. Start the app

```bash
docker compose -f deploy/docker-compose.yml up -d
```

Open **http://localhost:8080** in your browser. That's it.

### 4. Stopping / restarting

```bash
# Stop
docker compose -f deploy/docker-compose.yml down

# Start again
docker compose -f deploy/docker-compose.yml up -d

# View logs
docker compose -f deploy/docker-compose.yml logs -f
```

---

## Accessing from your phone (same Wi-Fi)

Barcode scanning requires HTTPS. To use it on your phone:

1. Find your computer's local IP address (e.g. `192.168.1.42`).
2. Set `SELF_SIGNED_TLS=true` in your `.env` file and restart.
3. Open `https://192.168.1.42:8443` on your phone.
4. Your browser will warn about the certificate — tap **Advanced → Proceed** to continue. This is safe on your home network.
5. When prompted, allow camera access for barcode scanning.

---

## Adding Google Login (optional)

If you want to require a Google account to access the app:

### Step 1 — Create a Google OAuth app

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → **APIs & Services → Credentials**
2. Click **Create Credentials → OAuth 2.0 Client ID**
3. Choose **Web application**
4. Under **Authorised redirect URIs**, add: `https://your-domain.com/auth/callback`
5. Click **Create** and copy the **Client ID** and **Client Secret**

### Step 2 — Generate a session secret

Open a terminal and run:

```bash
openssl rand -hex 32
```

Copy the output — this is your `SESSION_SECRET`.

### Step 3 — Update your `.env`

```
OAUTH_ENABLED=true
GOOGLE_CLIENT_ID=your-client-id-here
GOOGLE_CLIENT_SECRET=your-client-secret-here
SESSION_SECRET=paste-the-64-char-hex-string-here
BASE_URL=https://your-domain.com
OAUTH_ALLOWED_EMAILS=you@gmail.com,partner@gmail.com
```

Restart the app after saving.

---

## Backing up your data

All data is stored in a single SQLite file (`kitchen.db`). There are two ways to back it up:

**From the app:** Click the 💾 button in the top-right corner of the app. A `.db` file will download to your computer.

**From the server:**
```bash
cp /data/kitchen.db ~/kitchen-backup-$(date +%Y%m%d).db
```

To restore, stop the app, replace `kitchen.db` with your backup, and restart.

---

## Configuration reference

All settings go in your `.env` file:

| Variable | Default | Description |
|---|---|---|
| `DB_PATH` | `./kitchen.db` | Where to store the database file |
| `SELF_SIGNED_TLS` | — | Set to `true` to serve HTTPS on port 8443 (needed for phone barcode scanning) |
| `OAUTH_ENABLED` | — | Set to `true` to require Google login |
| `OAUTH_ALLOWED_EMAILS` | — | Comma-separated list of emails allowed to log in |
| `GOOGLE_CLIENT_ID` | — | From Google Cloud Console |
| `GOOGLE_CLIENT_SECRET` | — | From Google Cloud Console |
| `SESSION_SECRET` | — | Random string for securing login cookies (`openssl rand -hex 32`) |
| `BASE_URL` | — | Your app's public URL, e.g. `https://kitchen.example.com` |

---

## Units

The app supports 14 units across three categories:

| Category | Units |
|---|---|
| Weight | `g`, `kg`, `oz`, `lb` |
| Volume | `ml`, `L`, `tsp`, `tbsp`, `cup` |
| Count | `piece`, `clove`, `can`, `jar`, `bunch` |

Units in the same category can be converted automatically. Each pantry item has a **preferred unit** — this is used for display and for calculating how much of an item is low.

---

## Recipe import

Most major recipe sites work with the URL import. If a site blocks automatic fetching:

1. Open the recipe in your browser
2. Select all (Ctrl+A), copy (Ctrl+C)
3. Use the **Paste** tab in the Import dialog

---

## Running without Docker (developers)

```bash
# Plain HTTP on :8080
go run .

# HTTPS on :8443 with self-signed cert
SELF_SIGNED_TLS=true go run .

# Run tests
go test ./...
```

---

## License

MIT — see [LICENSE](LICENSE)
