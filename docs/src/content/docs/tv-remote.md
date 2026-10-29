---
title: Android TV remote (optional)
description: Open a title's Stremio detail page from ChatGPT using a tiny Android TV receiver and free Firebase Cloud Messaging.
order: 4
category: Guides
summary: Programmatic Firebase provisioning, three opt-in MCP tools, TV pairing, ADB sideloading, and background-launch troubleshooting.
---

This **opt-in, experimental** feature opens a movie or series details page in **Stremio for Android TV**, without automatically starting playback. Use it to say "Open Inception on my TV" from ChatGPT on your phone. You do **not** need a running laptop, home server, ADB connection, or a TV polling loop after installation.

**Nothing changes for existing users.** With `TV_OPEN_ENABLED` unset or anything other than the literal `true`, the MCP still exposes exactly its original five tools. No companion APK or Firebase project is required. The three TV tools and their public pairing endpoints are absent.

## How it works

```text
ChatGPT on phone → private MCP tool tv_open_title (Vercel)
                 → FCM HTTP v1 (Google Play Services)
                 → Android TV receiver (wakes on push)
                 → stremio:///detail/movie/tt1375666/tt1375666
                 → Stremio's native details page
```

Your MCP uses its **existing Upstash Redis** for short-lived pair codes and private device registration records. It sends a **data-only, high-priority, zero-TTL FCM message** so an old command isn't replayed when a TV reconnects. The APK does not run an always-on service, WebSocket, local HTTP listener, or periodic background job. The Firebase SDK and Google Play Services handle delivery. The app polls only while its pairing screen is visible.

**Requirements:** Android TV / Google TV with **Google Play Services and the Play Store installed** (Android 8+ for this APK), Stremio installed, network access, and a Firebase project with Cloud Messaging enabled. Fire OS devices without Google Play Services are not supported by this FCM build. The TV must be on, with Stremio available. The first sideload/setup can use ADB; normal use cannot.

## Cost

[Firebase Cloud Messaging is listed as **No-cost**](https://firebase.google.com/pricing) on both Firebase plans. The **Spark plan requires no payment method**. This integration uses no Firestore, Firebase Hosting, Cloud Functions, Analytics, or Cloud Storage. It reuses your Vercel API and Upstash account. Check those providers' quotas separately if your deployment is very busy; FCM itself has no per-push charge.

## 1. Set up Firebase, no paid plan required

You can provision everything programmatically with the **Firebase CLI** and **Google Cloud CLI** after signing into a Google account. Neither CLI needs to run on the TV. A coding agent can execute these commands for a user who grants authentication/approval. A brand-new Firebase project on the free **Spark** plan is enough.

Install the [Firebase CLI](https://firebase.google.com/docs/cli) and [gcloud CLI](https://cloud.google.com/sdk/docs/install), then sign in:

```bash
firebase login
gcloud auth login
```

**Optional one-command bootstrap:** The repository also provides a guarded, **preview-first** shell script that performs the project, app, service-account, and key-file setup below. It requires `jq`, the two authenticated CLIs, and explicit `--apply`; it never changes production Vercel or Firebase billing settings:

```bash
scripts/setup-tv-fcm.sh --project your-unique-fcm-project-id
scripts/setup-tv-fcm.sh --project your-unique-fcm-project-id --apply
```

You can instead follow the individual commands below if you want full manual control. The project creation and IAM steps still require Google-account permission.

Create a **globally unique** project ID (replace the example with your own). This step may require accepting project creation in your Google account:

```bash
export FCM_PROJECT_ID=your-unique-fcm-project-id
firebase projects:create "$FCM_PROJECT_ID" --display-name "Stremio TV Remote"
```

Register the Android package (package name is fixed by this sample APK) and note the **Firebase Android App ID** returned:

```bash
firebase apps:create ANDROID "Stremio TV Remote" \
  --package-name xyz.ashray.stremiotvremote --project "$FCM_PROJECT_ID"
```

Download that registered app's public configuration into the companion module:

```bash
firebase apps:sdkconfig ANDROID YOUR_FIREBASE_ANDROID_APP_ID \
  --project "$FCM_PROJECT_ID" \
  --out android-tv/app/google-services.json
```

Alternatively, download `google-services.json` from **Firebase console → Project settings → Your apps**. The file contains *public app identifiers* and an API key, **not** your server service-account private key. The repo ignores it to keep every fork's configuration local. The Gradle build parses it directly and initializes Firebase in the Java application; there is no Analytics dependency or Google Services Gradle plugin.

### Programmatically create a restricted server credential

The Go MCP backend uses the **FCM HTTP v1 API** and needs a Google service account with permission to send pushes. The following creates one specifically for this feature. Do not use your personal Google password or upload service-account JSON to Git.

```bash
gcloud services enable fcm.googleapis.com --project "$FCM_PROJECT_ID"

gcloud iam service-accounts create stremio-tv-sender \
  --project "$FCM_PROJECT_ID" \
  --display-name "Stremio TV MCP FCM sender"

export FCM_SA="stremio-tv-sender@$FCM_PROJECT_ID.iam.gserviceaccount.com"
gcloud projects add-iam-policy-binding "$FCM_PROJECT_ID" \
  --member "serviceAccount:$FCM_SA" \
  --role roles/firebasecloudmessaging.admin

umask 077
mkdir -p "$HOME/.config/stremio-tv-fcm"
gcloud iam service-accounts keys create \
  "$HOME/.config/stremio-tv-fcm/service-account.json" \
  --project "$FCM_PROJECT_ID" --iam-account "$FCM_SA"
```

The script creates an **offline private key file** on your machine. Keep it private and don't send it through chat or paste it into commands. Some organizations disable service-account key creation; in that case, configure an approved workload-identity flow instead of bypassing organizational policy. The provided Go implementation expects a JSON service-account credential in its Vercel environment.

## 2. Enable TV tools on your existing Vercel MCP

Set these **production server-side** environment variables on the **Go API project** (not the separate docs site):

| Variable | Value |
| --- | --- |
| `TV_OPEN_ENABLED` | `true` (exact literal; default is disabled) |
| `FCM_PROJECT_ID` | Your Firebase project ID |
| `FCM_SERVICE_ACCOUNT_JSON` | The **complete service-account JSON**, stored as a Vercel **Secret** |

Example with Vercel CLI (run from the repository root, with the correct Vercel project linked):

```bash
printf 'true' | vercel env add TV_OPEN_ENABLED production
printf '%s' "$FCM_PROJECT_ID" | vercel env add FCM_PROJECT_ID production
vercel env add FCM_SERVICE_ACCOUNT_JSON production --type secret \
  < "$HOME/.config/stremio-tv-fcm/service-account.json"
```

Deploy the Go branch **only when you're ready to enable the feature**. A redeployment is required after changing Vercel environment variables. In installations that don't enable it, no new MCP tools appear. The existing Upstash database is reused with **separate `:tv:` namespaced keys**, not modified catalog documents.

If using a locally managed process instead of Vercel, export those same three environment variables and your existing Redis configuration. Do not put service account JSON into `PUBLIC_BASE_URL`, the TV app, or your Stremio add-on manifest.

## 3. Build and sideload the Android TV companion

Use **JDK 17** and the **Android SDK 36** (the checked-in Gradle wrapper downloads Gradle 8.13). No Android Studio UI, Kotlin, Compose, or paid developer account is needed. On macOS, `ANDROID_HOME` commonly points to `~/Library/Android/sdk`.

```bash
cd android-tv
./gradlew :app:assembleDebug
```

The APK is at `android-tv/app/build/outputs/apk/debug/app-debug.apk` (from the repository root). The APK can **compile without** `google-services.json` for development tests, but **cannot pair or receive pushes** until rebuilt with a real Firebase config. Debug APKs are signed automatically for ADB sideloading.

To pre-fill your hosted MCP's HTTPS origin in the app, you can optionally include:

```bash
./gradlew :app:assembleDebug -PtvServerUrl=https://your-mcp.example.com
```

### Install using ADB

Enable **Developer options / Wireless debugging** on the TV. On modern Android TV, Wireless debugging shows a **pairing IP:port and pairing code**, followed by an **ADB connection port** (these are often different).

```bash
adb pair TV_IP:PAIRING_PORT PAIRING_CODE
adb connect TV_IP:ADB_PORT
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

For TVs exposing legacy network debugging at port 5555, `adb connect TV_IP:5555` can be used instead. Do not assume port 5555 is enabled on every TV.

Launch the pairing screen:

```bash
adb shell am start -n xyz.ashray.stremiotvremote/.MainActivity \
  --es server_url https://your-mcp.example.com
```

Use **only your public HTTPS origin**, never `/mcp?key=...` and never the private `STREMIO_FEED_TOKEN`. The app needs none of the MCP secrets. The TV displays an eight-digit code after obtaining an FCM registration token.

From ChatGPT using your connected MCP, say **"Pair my Android TV with code 12345678"** (use the actual TV code). The optional `tv_pair` tool approves it. The foreground app checks its pairing status every four seconds and securely stores a per-device credential. The code expires after ten minutes. After pairing, return to Stremio.

### Allow one-step background navigation

Android normally **restricts starting activities from the background**. To automatically switch to Stremio, grant the companion the special **Display over other apps** permission. On supported TVs, select **Allow opening Stremio from the background** in the companion's setup UI.

Some Android TV settings apps don't expose the permission screen. If so, while ADB is connected:

```bash
adb shell appops set xyz.ashray.stremiotvremote SYSTEM_ALERT_WINDOW allow
```

The app **does not draw any overlay**. The permission exists solely to attempt Android 12's background-activity-launch exemption. This behavior is **not verified on every TV firmware**; Android versions may change restrictions. If automatic navigation fails, an ordinary notification with an **Open in Stremio** action is attempted (where TV notifications are available), and the app offers **Open last requested title** for a one-click fallback.

Android 13+ also requires notification permission for notifications. The companion requests it when opened. On Android 15+, further OS behavior may differ. Sideloaded installation, overlay permission, and notification availability should be validated on the target TV.

After successful setup, disconnect ADB and close the pairing UI. There is **no need to leave it in the foreground** or keep your laptop connected.

## 4. Use ChatGPT

With `TV_OPEN_ENABLED=true`, three new tools appear:

| Tool | Usage |
| --- | --- |
| `tv_pair` | Approve the TV's eight-digit code. |
| `tv_devices` | List paired TVs, or explicitly remove one with `action=remove` and `device_id`. |
| `tv_open_title` | Send `media_type` (`movie` or `series`) and `imdb_id` (`tt...`). For one paired TV, `device_id` is optional. |

For example, the MCP input:

```json
{"media_type":"movie","imdb_id":"tt1375666"}
```

asks the TV to open `stremio:///detail/movie/tt1375666/tt1375666`. For a series, the episode/video ID is left empty to open the show's main detail page; no autoplay flag is set. Cinemeta and installed Stremio add-ons remain responsible for descriptions, IMDb ratings, trailers, episodes, and playback sources.

**Important:** The server returns `accepted_by_fcm` only after Google accepts the push. This is **not proof the TV received it or that Stremio opened successfully**. The TV must be on and connected, and zero-TTL commands intentionally won't be queued for offline delivery. If multiple TVs are paired, choose one of the `tv_devices` IDs.

To revoke a companion, ask the MCP to remove that device and reset local pairing inside its Android app. Uninstalling the app may eventually invalidate its FCM token but **does not** instantly delete the private paired-device record in Redis.

## Security and troubleshooting

- **No MCP key on TV:** Only `tv_pair` on the authenticated MCP can approve a TV. Public pairing endpoints cannot send commands, read device lists, or invoke Stremio. Device records and FCM tokens are private to your Redis instance.
- **Short-lived pairing:** Codes are eight digits, single-use, ten-minute expiry. The TV receives a separate 256-bit session credential to poll approval, then a distinct per-device secret for token refresh. Pair creation is per-address and globally rate-limited.
- **Narrow actions:** Push payloads contain only canonical IMDb IDs, `movie`/`series`, a command ID, and an issue time. No arbitrary URLs, autoplay, shell commands, streams, or app-control RPCs. The receiver rejects invalid/stale commands.
- **FCM errors:** `401/403` from Google's HTTP v1 API usually indicates a wrong service account, project or IAM role. An unregistered device token means the APK should be reopened/re-paired. Ensure Cloud Messaging API is enabled.
- **No push delivery:** Ensure Google Play Services is installed/enabled and the APK is built with the Firebase config for the *same project* configured in Vercel. Battery/firmware restrictions can delay data pushes.
- **No automatic navigation:** Grant Display over other apps, confirm Stremio is installed, and verify its deep link with ADB. Android firmware may still require manually opening the notification or last request.

```bash
adb shell am start -a android.intent.action.VIEW \
  -d 'stremio:///detail/movie/tt1375666/tt1375666'
```

This last command is a **manual diagnostic while setting up**, not part of normal operation. You can inspect errors with `adb logcat`, but no TV is needed to compile the APK or run Go tests.

Upstream: [Firebase Cloud Messaging pricing](https://firebase.google.com/pricing), [Android FCM setup](https://firebase.google.com/docs/cloud-messaging/android/get-started), [Android background-activity rules](https://developer.android.com/guide/components/activities/background-starts), [Stremio deep-link reference](https://stremio.github.io/stremio-addon-sdk/deep-links.html).
