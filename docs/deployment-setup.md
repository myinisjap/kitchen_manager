# Deployment Setup Guide

Steps to gather everything needed before running kitchen-manager in production.

---

## 1. Google OAuth Credentials

### Create a Google Cloud Project

1. Go to [console.cloud.google.com](https://console.cloud.google.com)
2. Click the project dropdown (top left) → **New Project**
3. Name it anything (e.g. `kitchen-manager`) → **Create**
4. Make sure the new project is selected in the dropdown

### Configure the OAuth Consent Screen

1. In the left sidebar: **APIs & Services → OAuth consent screen**
2. Choose **External** → **Create**
3. Fill in required fields:
   - App name: `Kitchen Manager`
   - User support email: your Gmail address
   - Developer contact email: your Gmail address
4. Click **Save and Continue** through Scopes (no changes needed) and Test Users
5. On the Test Users step, click **Add Users** and add every Gmail address that should have access
6. **Save and Continue** → **Back to Dashboard**

> Note: While the app is in "Testing" mode, only the emails you add as test users can log in. This is fine for personal use — you never need to publish the app.

### Create OAuth Credentials

1. **APIs & Services → Credentials**
2. **Create Credentials → OAuth 2.0 Client ID**
3. Application type: **Web application**
4. Name: `kitchen-manager`
5. Under **Authorized redirect URIs**, click **Add URI** and enter:
   ```
   https://myinisjap.tplinkdns.com/auth/callback
   ```
6. Click **Create**
7. Copy the **Client ID** and **Client Secret** — these go into `.env` as `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`

### Generate a Session Secret

Run this on your server to generate a random 32-byte secret:
```bash
openssl rand -hex 32
```
This goes into `.env` as `SESSION_SECRET`.

---

## 2. TP-Link DDNS & Router Setup

### Confirm Your DDNS Hostname

Your TP-Link router should already be registered with a DDNS hostname. Confirm it's active:

1. Log into your TP-Link router admin panel (usually `192.168.0.1` or `192.168.1.1`)
2. Go to **Advanced → Network → Dynamic DNS**
3. Confirm the hostname (`myinisjap.tplinkdns.com`) is listed and the status shows as connected

### Port Forward 80 and 443

Caddy needs ports 80 and 443 reachable from the internet to provision the Let's Encrypt certificate and serve traffic.

1. In the router admin panel: **Advanced → NAT Forwarding → Virtual Servers** (may also be called **Port Forwarding**)
2. Add two rules:

   | Service Port | Internal IP | Internal Port | Protocol |
   |-------------|-------------|---------------|----------|
   | 80           | (IP of your server) | 80 | TCP |
   | 443          | (IP of your server) | 443 | TCP |

3. To find your server's local IP:
   ```bash
   hostname -I | awk '{print $1}'
   ```

### Assign a Static Local IP (Recommended)

If your server gets a new IP from DHCP, the port forward rules will break. Reserve a static IP:

1. Router admin → **Advanced → Network → DHCP Server → Address Reservation**
2. Add a reservation using your server's MAC address and choose a fixed IP (e.g. `192.168.0.100`)

To find the MAC address:
```bash
ip link show | grep -A1 eth0 | grep ether
```
(Replace `eth0` with your actual interface name from `ip link show`)

---

## 3. Caddyfile

The `Caddyfile` in the repo is already configured. No changes needed unless your hostname is different:

```
myinisjap.tplinkdns.com {
    reverse_proxy app:8080
}
```

Caddy automatically:
- Provisions a Let's Encrypt certificate for the hostname on first request
- Redirects HTTP → HTTPS
- Renews the certificate before it expires

The only requirement is that port 80 is reachable from the internet when Caddy first starts (for the ACME HTTP-01 challenge).

---

## 4. Final .env Checklist

```
DB_PATH=/data/kitchen.db
OAUTH_ENABLED=true
OAUTH_ALLOWED_EMAILS=you@gmail.com,partner@gmail.com
GOOGLE_CLIENT_ID=<from Google Cloud Console>
GOOGLE_CLIENT_SECRET=<from Google Cloud Console>
SESSION_SECRET=<from openssl rand -hex 32>
BASE_URL=https://myinisjap.tplinkdns.com
```

---

## 5. First Run

```bash
git clone <repo> kitchen-manager
cd kitchen-manager
cp .env.example .env
# fill in .env values per above
docker compose up -d
```

Visit `https://myinisjap.tplinkdns.com` — Caddy will provision the cert on the first request (may take 10–30 seconds). You'll be redirected to Google to log in.

### Verify the cert is working

```bash
curl -I https://myinisjap.tplinkdns.com
```

Should return `HTTP/2 200` (or a redirect) with no certificate errors.
