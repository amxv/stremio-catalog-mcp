---
title: Android TV remote (optional)
description: Open a title's Stremio detail page from ChatGPT using a tiny Android TV receiver and free Firebase Cloud Messaging.
order: 4
category: Guides
summary: One optional MCP tool, private browser-based single-TV pairing, Firebase provisioning, ADB sideloading, and troubleshooting.
---

This **opt-in, experimental** feature opens a movie or series details page in **Stremio for Android TV**, without automatically starting playback. Use it to say "Open Inception on my TV" from ChatGPT on your phone. You do **not** need a running laptop, home server, ADB connection, or a TV polling loop after installation.

**Nothing changes for existing users.** With `TV_OPEN_ENABLED` unset or anything other than the literal `true`, the MCP still exposes exactly its original five tools. No companion APK or Firebase project is required. The single `tv_open_title` MCP tool, private setup page, and TV pairing endpoints are absent.

## Start here: give these instructions to your coding agent

If you already self-host Stremio Catalog MCP, **do not redeploy the whole service from scratch**: the TV remote is an optional add-on for your existing Vercel API and Upstash Redis. New users should first follow the [core self-hosting Quickstart](/docs/quickstart). Then give your agent this request:

> Set up the **optional Android TV remote** from https://stremio-catalog-mcp.ashray.xyz/docs/tv-remote on my existing Stremio Catalog MCP. Work in **my** Vercel and Firebase accounts, not yours. Use a **separate Firebase Spark (no-cost) project**, register the exact Android package in the repo, securely create a narrowly scoped FCM service account, and enable only the four documented environment variables on my **Go API Vercel project**. Build the companion with its real `google-services.json`, connect to **my Android TV over ADB**, sideload it, authorize the one-TV pairing via the private `/tv/setup` page, and grant background-opening permission with my consent. Verify a direct Stremio deep link **and** a real FCM-triggered navigation from the MCP, then disconnect ADB. Never print or commit credentials, upgrade billing, overwrite unrelated Vercel variables, autoplay anything, or claim delivery based only on Google's acknowledgment. Let me do any required Google sign-in, TV developer-option confirmation, or final ChatGPT connection. Don't stop at an APK build; verify the actual TV when it's available.

### What the agent should finish with

| Check | Expected result |
| --- | --- |
| Core MCP unchanged | Five tools if TV is disabled; **six** when enabled, with only `tv_open_title` added |
| Cloud project | Firebase **Spark**, FCM API enabled, registered Android app, no paid products or billing added |
| Vercel | Existing Go API deployed and responding; its FCM JSON and setup key stored as **Secrets** |
| TV | APK installed; **one** paired TV appears at `https://YOUR_API_ORIGIN/tv/setup` |
| Functional test | Stremio shows the **requested movie/series details**, without starting playback |
| Cleanup | ADB disconnected; no permanent Mac/TV connection, polling daemon, or agent process |

**There are two separate codes:** an optional **ADB wireless debugging pairing code** (used once to authorize your laptop to the TV), and an **eight-digit companion pairing code** (used later in `/tv/setup`). Neither is your private `MCP_API_KEY`, `TV_SETUP_KEY`, or Firebase service-account key.

## How it works

```text
ChatGPT on phone → private MCP tool tv_open_title (Vercel)
                 → FCM HTTP v1 (Google Play Services)
                 → Android TV receiver (wakes on push)
                 → stremio:///detail/movie/tt1375666/tt1375666
                 → Stremio's native details page
```

Your MCP uses its **existing Upstash Redis** for short-lived pair codes and private device registration records. It sends a **data-only, high-priority, zero-TTL FCM message** so an old command isn't replayed when a TV reconnects. The APK does not run an always-on service, WebSocket, local HTTP listener, or periodic background job. The Firebase SDK and Google Play Services handle delivery. The app polls only while its pairing screen is visible.

**Requirements:** Android TV / Google TV with **Google Play Services and the Play Store installed** (Android 8+ for this APK), Stremio installed, network access, and a Firebase project with Cloud Messaging enabled. Fire OS devices without Google Play Services are not supported by this FCM build. The TV must be on, with Stremio available. First-time sideload/setup can use ADB; normal use **does not need ADB**.

## Cost

[Firebase Cloud Messaging is listed as **No-cost**](https://firebase.google.com/pricing) on both Firebase plans. The **Spark plan requires no payment method**. This integration uses no Firestore, Firebase Hosting, Cloud Functions, Analytics, or Cloud Storage. It reuses your Vercel API and Upstash account. Check those providers' quotas separately if your deployment is very busy; FCM itself has no per-push charge.

## 1. Set up Firebase, no paid plan required

You can provision everything programmatically with the **Firebase CLI** and **Google Cloud CLI** after signing into a Google account. Neither CLI needs to run on the TV. A coding agent can execute these commands for a user who grants authentication/approval. A brand-new Firebase project on the free **Spark** plan is enough.

**Agent handoff:** The user can use an existing Google account—even one that already runs a billed Maps API project—while creating a **new, separate Firebase project**. Ask the user to sign in through their browser or the two CLIs; do not reuse the Maps project or its billing. In the Firebase console, choose **Create a project**, disable optional **Google Analytics** and **Gemini in Firebase**, and verify the project overview says **Spark / No-cost ($0/month)**. Creating a Google Cloud project and registering an Android app here do **not** require a Play Store developer account. Do not add Firestore, Cloud Functions, Cloud Run, or a Google Cloud billing account.

Install the [Firebase CLI](https://firebase.google.com/docs/cli) and [gcloud CLI](https://cloud.google.com/sdk/docs/install), then sign in:

```bash
firebase login
gcloud auth login
firebase projects:list  # Confirm the signed-in account, not merely a browser session.
```

If the agent runs on a remote machine, `firebase login --no-localhost` is available; let the user finish the sign-in. The Firebase console is also a supported manual alternative when the CLIs are unavailable. In either case, **select the intended Google account and verify the project ID before creating or changing resources**.

**Optional one-command bootstrap:** The repository also provides a guarded, **preview-first** shell script that performs the project, app, service-account, and key-file setup below. Run it **from the repository root**. It requires `jq`, the two authenticated CLIs, and explicit `--apply`; it never changes production Vercel, sets up the TV, or changes Firebase billing settings:

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

If the user created the project in the browser, **reuse its exact project ID** instead of running `projects:create` again. Check that it is still on Spark and that it is not one of the user's other Google Cloud projects.

Register the Android package (package name is fixed by this sample APK; changing it requires changing the Android sources) and note the **Firebase Android App ID** returned:

```bash
firebase apps:create ANDROID "Stremio TV Remote" \
  --package-name xyz.ashray.stremiotvremote --project "$FCM_PROJECT_ID"
```

If an app with this exact package already exists, use `firebase apps:list --project "$FCM_PROJECT_ID"` and reuse its **Android** App ID. Do not register a duplicate or mistake the project ID for the Android App ID.

Download that registered app's public configuration into the companion module:

```bash
firebase apps:sdkconfig ANDROID YOUR_FIREBASE_ANDROID_APP_ID \
  --project "$FCM_PROJECT_ID" \
  --out android-tv/app/google-services.json
```

Alternatively, download `google-services.json` from **Firebase console → Project settings → Your apps**. Verify its `project_info.project_id` matches `$FCM_PROJECT_ID`, and that its Android client package is `xyz.ashray.stremiotvremote`. Place it at **`android-tv/app/google-services.json`**, not the repo root. The file contains *public app identifiers* and an API key, **not** your server service-account private key. The repo ignores it to keep every fork's configuration local. The Gradle build parses it directly and initializes Firebase in the Java application; there is no Analytics dependency or Google Services Gradle plugin.

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

## 2. Opt in on the user's Vercel Go API project

**Do this only if the user wants TV control.** The default five-tool MCP needs none of these variables. Use the **already configured Go API Vercel project**, with its existing MCP key, Redis REST credentials, and Stremio settings. Do not create another Vercel API project, change the docs-site project, replace existing secrets, or require the user to reconfigure Stremio.

From the **repository root**, confirm which Vercel project you are about to change:

```bash
vercel whoami
vercel project inspect YOUR_EXISTING_GO_API_PROJECT
# If this directory is not linked to it yet:
vercel link --yes --project YOUR_EXISTING_GO_API_PROJECT
cat .vercel/project.json  # Project metadata only; never display private env values.
```

For a newly self-hosted installation, first finish the [core Vercel + Upstash deployment](/docs/deployment). If the API is running on a protected Vercel **Preview** URL, note that Vercel Authentication may prevent the Android TV from reaching its endpoints. Use the existing public **production HTTPS API origin** (custom domain or Vercel production URL); `/mcp?key=...` is a private *ChatGPT connector* URL, **not** the origin to enter on the TV.

Set these **production server-side** environment variables on the linked **Go API project** (not the docs site):

| Variable | Value |
| --- | --- |
| `TV_OPEN_ENABLED` | `true` (exact literal; default is disabled) |
| `FCM_PROJECT_ID` | Your Firebase project ID |
| `FCM_SERVICE_ACCOUNT_JSON` | The **complete service-account JSON**, stored as a Vercel **Secret** |
| `TV_SETUP_KEY` | Separate random secret (at least 32 characters) protecting the private TV setup web page |

Example with Vercel CLI (run **from the repo root**, after confirming `.vercel/project.json` is the intended Go API project). Use `printf` or file redirection: don't add a trailing newline to a literal `true` value or inadvertently print sensitive JSON in your terminal history.

```bash
# Use the actual private sender JSON from step 1 (or the bootstrap script's
# $HOME/.config/stremio-tv-fcm/$FCM_PROJECT_ID-sender.json path).
FCM_KEY_FILE="$HOME/.config/stremio-tv-fcm/service-account.json"
if [ -s "$HOME/.config/stremio-tv-fcm/$FCM_PROJECT_ID-sender.json" ]; then
  FCM_KEY_FILE="$HOME/.config/stremio-tv-fcm/$FCM_PROJECT_ID-sender.json"
fi
test -s "$FCM_KEY_FILE" || { printf 'FCM sender JSON missing\n' >&2; exit 1; }

printf '%s' "$FCM_PROJECT_ID" | vercel env add FCM_PROJECT_ID \
  --environment production --no-sensitive --yes
vercel env add FCM_SERVICE_ACCOUNT_JSON \
  --environment production --sensitive --yes < "$FCM_KEY_FILE"

# Create a separate private web-setup credential, not the MCP key.
umask 077
mkdir -p "$HOME/.config/stremio-tv-fcm"
TV_SETUP_KEY_FILE="$HOME/.config/stremio-tv-fcm/tv-setup-key"
if [ ! -e "$TV_SETUP_KEY_FILE" ]; then
  python3 -c 'import secrets; print(secrets.token_urlsafe(32), end="")' \
    > "$TV_SETUP_KEY_FILE"
fi
chmod 600 "$TV_SETUP_KEY_FILE"
vercel env add TV_SETUP_KEY \
  --environment production --sensitive --yes < "$TV_SETUP_KEY_FILE"

# This literal is the opt-in switch. Don't set it unless the user requested TV.
printf 'true' | vercel env add TV_OPEN_ENABLED \
  --environment production --no-sensitive --yes
```

The example automatically selects the sender JSON written by `scripts/setup-tv-fcm.sh` when present, otherwise the manual `service-account.json` path. If any variable already exists, **inspect its environment and value ownership first** instead of blindly overwriting it. Keep `FCM_SERVICE_ACCOUNT_JSON` and `TV_SETUP_KEY` as **Vercel Secrets**, never plaintext CLI arguments, GitHub secrets exposed to client code, or values in `.env.example`.

After the user authorizes production deployment, deploy the **Go API from the repository root**:

```bash
make check
vercel deploy --prod --yes
vercel env ls  # Verify names, environments and Secret/Config types only.
```

The new environment variables only take effect on a **new deployment**. The existing Upstash Redis database is reused with **separate `:tv:` namespaced keys**; catalog documents and Stremio account credentials are untouched. Check that `https://YOUR_API_ORIGIN/tv/setup` responds with HTTP 200 and that a request without the setup bearer credential to `/tv/admin` responds with HTTP 401. An unauthenticated `/mcp` still responds with HTTP 401. The authenticated MCP `tools/list` should report **six tools**, not eight: the original five plus `tv_open_title`.

If using a locally managed process instead of Vercel, export those same four environment variables and your existing Redis configuration. Do not put a service-account private key into `PUBLIC_BASE_URL`, the TV app, or your Stremio add-on manifest.

## 3. Build and sideload the Android TV companion

Use **JDK 17** and the **Android SDK 36** (the checked-in Gradle wrapper downloads Gradle 8.13). No Android Studio UI, Kotlin, Compose, or paid developer account is needed. On macOS, `ANDROID_HOME` commonly points to `~/Library/Android/sdk`.

```bash
cd android-tv
export JAVA_HOME="$(/usr/libexec/java_home -v 17)"  # macOS; set JDK 17 path manually elsewhere.
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"  # macOS default.
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

The APK is at **`android-tv/app/build/outputs/apk/debug/app-debug.apk` from the repository root** (or `app/build/outputs/apk/debug/app-debug.apk` while inside `android-tv`). The APK can **compile without** `google-services.json` for development tests, but **cannot pair or receive pushes** until rebuilt with a real Firebase config. The build must use the **same Firebase project** as `FCM_PROJECT_ID` on Vercel. Debug APKs are signed automatically for ADB sideloading.

To pre-fill your hosted MCP's HTTPS origin in the app, you can optionally include:

```bash
./gradlew :app:assembleDebug -PtvServerUrl=https://YOUR_PUBLIC_API_ORIGIN
```

If you already built an APK before downloading `google-services.json`, **rebuild it**. It's fine to keep using the checked-in debug signing key for local testing, but don't distribute a debug APK as a Play Store release.

### Connect and verify the TV over ADB

**Agent: don't assume the device IP or debugging port.** First ask the user to turn on the TV, check its **Settings → Network** IP address, and enable **Developer options** and the appropriate **USB/network or Wireless debugging** option. On modern Android TV / Google TV, **Wireless debugging → Pair device with pairing code** shows a temporary *ADB pairing* IP:port and a six-digit code. After pairing, the TV displays a separate **ADB connect** port. Both devices must be on the same local network. The user may need to accept an authorization prompt on the TV.

```bash
adb version
adb mdns services  # Optional discovery; not every TV advertises a service.

# Wireless debugging when the TV offers a pairing code:
adb pair TV_IP:PAIRING_PORT ADB_PAIRING_CODE
adb connect TV_IP:ADB_CONNECT_PORT

# Or, for TCL/other TVs that explicitly expose legacy network ADB port 5555:
adb connect TV_IP:5555

adb devices -l
```

**Use whichever connection method actually applies**, not both. If neither works, check developer options and Wi-Fi isolation with the user before probing other LAN devices. An ADB pairing code is **not** the eight-digit companion pairing code used later. Copy the exact device **serial** shown by `adb devices -l`, then keep using `-s` so commands cannot accidentally target another Android device:

```bash
ADB_SERIAL="TV_IP:ADB_CONNECT_PORT"  # For legacy ADB, use TV_IP:5555.
adb -s "$ADB_SERIAL" shell getprop ro.product.manufacturer
adb -s "$ADB_SERIAL" shell getprop ro.product.model
adb -s "$ADB_SERIAL" shell getprop ro.build.version.release
adb -s "$ADB_SERIAL" shell pm list packages | grep -F com.stremio.one

# Run from android-tv/ after building:
adb -s "$ADB_SERIAL" install -r app/build/outputs/apk/debug/app-debug.apk
```

On the user's TV, Stremio's Android TV package is commonly `com.stremio.one`. If the package isn't installed, ask the user to install or sign in to Stremio before testing. If `adb install` fails, inspect the error and verify the APK is really built with the correct Firebase config; do not delete unrelated TV data.

### Start pairing from the companion app

Launch the receiver's setup UI, pre-filling the **public HTTPS API origin**:

```bash
adb -s "$ADB_SERIAL" shell am start \
  -n xyz.ashray.stremiotvremote/.MainActivity \
  --es server_url https://YOUR_PUBLIC_API_ORIGIN
```

**Never** enter the private `/mcp?key=...` URL or `STREMIO_FEED_TOKEN` on the TV; the app requires neither. Focus **Start pairing** with the remote and select it. An agent with ADB permission can inspect the screen's accessibility text without taking screenshots or using OCR:

```bash
adb -s "$ADB_SERIAL" shell uiautomator dump /sdcard/tv-setup.xml
adb -s "$ADB_SERIAL" shell cat /sdcard/tv-setup.xml
# If Start pairing has keyboard focus, KEYCODE_DPAD_CENTER activates it:
adb -s "$ADB_SERIAL" shell input keyevent KEYCODE_DPAD_CENTER
```

The companion requests an FCM token and then displays an **eight-digit code**. It will say `Pair code: ...`; if it shows an error instead, check its network/Google Play Services and the API origin. Do **not** copy the public Firebase Android config into Vercel in place of the service-account JSON.

On your **phone or laptop**, open **`https://YOUR_PUBLIC_API_ORIGIN/tv/setup`** and enter the separate `TV_SETUP_KEY` created in step 2. This setup key is **not** the MCP URL key. The page holds it only in the current tab's memory and sends it to the same-origin server in an authorization header; it is never in the URL or browser storage. On macOS, an agent can safely copy the key to the clipboard **without printing it**:

```bash
pbcopy < "$HOME/.config/stremio-tv-fcm/tv-setup-key"
```

Enter the TV's actual eight-digit companion code and select **Pair this TV**. The foreground app polls its pairing status every four seconds, confirms **Paired TV**, and securely stores a per-device credential. Pair codes expire after **ten minutes**, so generate a fresh code if you hit an expiry. In the setup page, the paired-device list should now contain **exactly one** TV.

**Agent-only alternative to clicking the page:** If the user explicitly authorized automated pairing, the agent may call the same authenticated setup endpoint locally. The Python example below reads the setup credential from its protected file instead of placing it in a CLI argument or printing it. Run this from the laptop, **not** the TV, using the real eight-digit code:

```bash
export TV_API_ORIGIN=https://YOUR_PUBLIC_API_ORIGIN
export TV_PAIR_CODE=12345678  # Replace with the eight-digit companion code on the TV.
python3 - <<'PY'
import json, os, urllib.request
from pathlib import Path

key = (Path.home() / '.config/stremio-tv-fcm/tv-setup-key').read_text()
payload = json.dumps({'action': 'pair', 'code': os.environ['TV_PAIR_CODE']}).encode()
req = urllib.request.Request(os.environ['TV_API_ORIGIN'] + '/tv/admin',
    data=payload, method='POST',
    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key})
with urllib.request.urlopen(req, timeout=20) as response:
    result = json.load(response)
    print('Paired TVs:', [tv['name'] for tv in result['devices']])
    assert len(result['devices']) == 1 and result['max_devices'] == 1
PY
unset TV_PAIR_CODE
```

**Never send an `open_title` action to `/tv/admin`**: it only accepts authenticated `list`, `pair`, and `remove` device-management actions. Actual playback/navigation requests must go through the **private authenticated MCP tool**. The admin key, FCM registration token, and per-device credential are never returned to the model by `tv_open_title`.

You may pair **one TV only**. To replace it, use **Revoke pairing** on the same private setup page, then reset local pairing inside the TV app and pair again. Existing versions of this MCP that already paired multiple devices can revoke their legacy entries from the setup page before using `tv_open_title`.

### Allow one-step background navigation

Android normally **restricts starting activities from the background**. To automatically switch to Stremio, the user must authorize the companion's special **Display over other apps** permission. On supported TVs, select **Allow opening Stremio from the background** in the companion's setup UI.

Some Android TV settings apps don't expose the permission screen. If so, while ADB is connected:

```bash
adb -s "$ADB_SERIAL" shell appops set xyz.ashray.stremiotvremote SYSTEM_ALERT_WINDOW allow
adb -s "$ADB_SERIAL" shell appops get xyz.ashray.stremiotvremote SYSTEM_ALERT_WINDOW
```

The app **does not draw any overlay**. The permission exists solely to attempt Android 12's background-activity-launch exemption. This behavior is **not verified on every TV firmware**; Android versions may change restrictions. If automatic navigation fails, an ordinary notification with an **Open in Stremio** action is attempted (where TV notifications are available), and the app offers **Open last requested title** for a one-click fallback.

Android 13+ also requires notification permission for notifications. The companion requests it when opened. On Android 15+, further OS behavior may differ. Sideloaded installation, overlay permission, and notification availability should be validated on the target TV.

After testing, return to Stremio and disconnect ADB using `adb disconnect TV_IP:ADB_CONNECT_PORT` (or `adb disconnect TV_IP:5555` for legacy ADB). The user may also turn off wireless debugging again. There is **no need to keep the TV companion visible, leave ADB enabled, or keep the laptop connected**.

## 4. Use ChatGPT

With `TV_OPEN_ENABLED=true`, there is **exactly one additional MCP tool**, `tv_open_title`. It accepts `media_type` (`movie` or `series`) and an IMDb ID (`imdb_id`, `tt...`). There is no device selection, pairing, or device-management surface in MCP. With the TV feature disabled, the original five tools remain unchanged.

Have the user refresh or reconnect their **existing** custom MCP connection in ChatGPT if its tool list still shows only the five default tools after the new deployment. Don't create a second connector just to discover the added tool, and don't expose device pairing as a model tool.

For example, the MCP input:

```json
{"media_type":"movie","imdb_id":"tt1375666"}
```

asks the TV to open `stremio:///detail/movie/tt1375666/tt1375666`. For a series, the episode/video ID is left empty to open the show's main detail page; no autoplay flag is set. Cinemeta and installed Stremio add-ons remain responsible for descriptions, IMDb ratings, trailers, episodes, and playback sources.

**Important:** The server returns `accepted_by_fcm` only after Google accepts the push. This is **not proof the TV received it or that Stremio opened successfully**. The TV must be on and connected, and zero-TTL commands intentionally won't be queued for offline delivery. A single paired TV is selected automatically.

### Prove it works on the actual TV (agent acceptance test)

The app can be installed and paired successfully **even if Android refuses background navigation**. A coding agent should test both paths while the user is available:

**First, verify that the installed Stremio handles a direct detail link.** This diagnostic opens a details page, not playback:

```bash
adb -s "$ADB_SERIAL" shell am start -W -a android.intent.action.VIEW \
  -d 'stremio:///detail/movie/tt1375666/tt1375666'
adb -s "$ADB_SERIAL" shell dumpsys activity activities | grep mResumedActivity
adb -s "$ADB_SERIAL" shell uiautomator dump /sdcard/stremio-detail.xml
adb -s "$ADB_SERIAL" shell cat /sdcard/stremio-detail.xml
```

Expect `com.stremio.one` in the resumed activity and **Inception (2010)** in the UI hierarchy. You can try a series with `stremio:///detail/series/tt0903747/` (**Breaking Bad**). The series main page does not select an episode or play a stream.

**Next, test the complete MCP → FCM → TV path.** While Stremio is showing a *different* title, invoke the authenticated MCP `tv_open_title` with `media_type: "series", imdb_id: "tt0903747"`. **Do not run another `adb shell am start` for this test.** Observe whether Stremio itself changes to **Breaking Bad (2008–2013)**. Verify the visible TV screen or its accessibility hierarchy. If Google reports `accepted_by_fcm` but the screen stays unchanged, the end-to-end test **failed**; investigate permission, push delivery, Stremio URI handling, and the TV logs.

**Finally, verify ADB is unnecessary:** `adb disconnect "$ADB_SERIAL"`, invoke `tv_open_title` for another title via MCP, and check the TV screen directly (or reconnect ADB only *afterward* to read the UI hierarchy). The agent should report these as **separate observations**: API accepted, TV received/opened, and no-ADB navigation. Once finished, disconnect ADB again. There is no perpetual Mac background service required.

For debugging only, with the user's permission:

```bash
adb -s "$ADB_SERIAL" logcat -d -t 600 -v brief \
  | grep -E 'FirebaseMessaging|ActivityTaskManager|ActivityTaskSupervisor|stremiotvremote'
adb -s "$ADB_SERIAL" shell dumpsys meminfo xyz.ashray.stremiotvremote
```

Android may keep the receiver **process cached** after a push. That doesn't mean an always-running service is active: measure proportional set size (PSS) and CPU over time before drawing conclusions. Don't `force-stop` the companion as part of a delivery test, since Android can then suppress FCM until the app is launched again.

To revoke a companion, use the private `/tv/setup` page, then reset local pairing inside its Android app. Uninstalling the app may eventually invalidate its FCM token but **does not** instantly delete the private paired-device record in Redis.

## Security and troubleshooting

- **No MCP key on TV:** A separate `TV_SETUP_KEY` authenticates browser-based pairing and revocation. The public pairing endpoints cannot approve devices, read paired-device lists, or invoke Stremio. Only the authenticated MCP can send an open-title command. Device records and FCM tokens remain private in Redis. The `/tv/admin` endpoint requires a bearer key (not a query-string token) and disallows cross-origin requests.
- **Single-TV enforcement:** Redis atomically refuses approval of a second paired device, even if two codes are approved concurrently.
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
