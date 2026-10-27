# Schwaber: Charles Schwab API & Roth IRA Setup Guide

This guide walks you through every step required to obtain API access from Charles Schwab, configure your environment, link your **ROTH IRA** account, and safely execute trades.

---

## Step 1: Create a Schwab Developer Account

1. Go to the [Schwab Developer Portal](https://developer.schwab.com/).
2. Click **Log In** or **Register** to create a Developer account (you can use your standard Schwab login credentials).
3. Complete any required developer registration agreements.

---

## Step 2: Register a Developer Application

1. In the Schwab Developer Portal dashboard, navigate to **Apps** > **Create an App**.
2. Fill out the application details:
   - **App Name**: e.g., `Schwaber` or `MyTradingBot`
   - **Callback URL**: `https://127.0.0.1` (or `https://127.0.0.1:8182`)
   - **APIs / Products**: Select **Trader API - Individual** and **Market Data API**.
3. Submit the app registration.
4. **Important**: Your app status may initially show as `Approved - Pending`. It usually changes to `Ready For Use` within a few minutes (or up to 24-48 hours during off-market hours).
5. Once `Ready For Use`, copy:
   - **App Key** (Client ID)
   - **Secret** (Client Secret)

---

## Step 3: Configure Environment Variables

Schwaber does not read a `.env` file. It reads its settings from the config object `gs://<bucket>/config/env` at start-up (needs `gcloud auth application-default login`), and a variable already exported in your shell wins over it. Put your credentials in the object with `deploy_gae/config.sh`:

   ```bash
   deploy_gae/config.sh set SCHWAB_APP_KEY=your_actual_app_key SCHWAB_APP_SECRET=your_actual_app_secret \
     SCHWAB_CALLBACK_URL=https://127.0.0.1 SCHWAB_TOKEN_PATH=token.json

   # Optional: if you know your Roth IRA account number (e.g. 12345678)
   deploy_gae/config.sh set SCHWAB_ROTH_ACCOUNT_NUMBER=12345678

   # Default to dry-run mode (set to false only for live executions)
   deploy_gae/config.sh set DRY_RUN_DEFAULT=true
   ```

   Build the CLI first with `make build`; it is `./bin/schwaber`.

---

## Step 4: Complete Initial OAuth Authentication

Build the binary if you haven't already:
```bash
make build
```

Run the interactive setup wizard:

```bash
./bin/schwaber auth
```

### What happens during authentication:
1. The script generates your unique Schwab OAuth authorization URL and attempts to open it in your default web browser.
2. Log into Schwab and **select the accounts** you want the API to access (ensure your **Roth IRA** is checked).
3. Click **Agree and Continue**.
4. Schwab redirects to your Callback URL (`https://127.0.0.1/?code=...`). Your browser might say *"This site can't be reached"* — **this is completely normal**.
5. Copy the **entire URL from your browser's address bar** and paste it into the terminal prompt.
6. The script exchanges the code for your initial OAuth tokens and saves them securely in `token.json`.

> [!NOTE]
> - **Access Tokens** expire every 30 minutes. The system **automatically refreshes** them in the background.
> - **Refresh Tokens** are valid for 7 days. If you do not trade for over 7 days, you will simply re-run `./bin/schwaber auth`.

---

## Step 5: Verify Your Accounts & Roth IRA

Run the account inspector to verify connection and identify your Roth IRA:

```bash
./bin/schwaber accounts
```

To see existing holdings in your accounts:
```bash
./bin/schwaber accounts --positions
```

If multiple IRAs are detected or your Roth IRA is not automatically flagged as default, copy its account number and set it in the config object:
```bash
deploy_gae/config.sh set SCHWAB_ROTH_ACCOUNT_NUMBER=12345678
```

---

## Step 6: Test Pre-Trade Guardrails (Dry-Run)

Before executing live orders, test with `--dry-run`:

```bash
# Preview buying 1 share of VOO (dry-run mode)
./bin/schwaber buy -symbol VOO -qty 1 --dry-run

# Preview a limit buy order
./bin/schwaber buy -symbol SCHD -qty 10 -price 80.00 --dry-run
```

The system will:
1. Check your Roth IRA cash balance and buying power.
2. Refuse a buy that exceeds buying power, or a sell of more shares than you hold.
3. Display a rich preview table with estimated costs.
4. Output the exact Schwab API JSON payload without submitting anything to the market.

---

## Roth IRA Specific Trading Rules & Considerations

> [!IMPORTANT]
> - **Cash Account Settlement (T+1)**: Standard equity/ETF trades settle in 1 business day (T+1). Buying with unsettled funds and selling before settlement triggers a **Good Faith Violation (GFV)**.
> - **No Short Selling**: Short selling is prohibited in Roth IRAs. The safety guard will reject any sell order for shares you do not currently hold.
> - **No Margin Borrowing**: You can only trade with available cash buying power.
