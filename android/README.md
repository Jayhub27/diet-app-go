# DietApp for Android

A thin native Android client for DietApp — hosted cloud app or self-hosted server.

## What it is

- First launch: **DietApp Cloud** (opens the hosted web app in Chrome Custom Tabs)
  or **My server** (WebView + server URL)
- Self-hosted mode: WebView loads your DietApp server URL
- Native camera + gallery bridge for the ingredient scanner (`/scan`)
- Server URL stored on-device
- Long-press the web page in server mode to switch to Cloud or change the URL
- Back button navigates web history

## Requirements

- Android Studio Hedgehog (2023.1.1) or newer
- Android SDK 24+ (Android 7.0+)
- Cloud mode: any browser that supports Custom Tabs (Chrome recommended)
- Server mode: a running DietApp server reachable from the phone (same Wi-Fi, or a public URL via tunnel/reverse proxy)

## Build

1. Open this `android/` directory in Android Studio.
2. Let Gradle sync (Android Studio will download the wrapper on first sync).
3. Connect a device (USB debugging) or start an emulator.
4. Run ▶ — the app installs and prompts for your server URL.

To build a signed release from CI, run the **Android APK** workflow. It uses
the `ANDROID_KEYSTORE_*` secrets and uploads `dietapp-android.apk`. Local
`assembleRelease` without those env vars produces an unsigned APK.

Version 1.1.0 changes the signing key; uninstall v1.0.0 once before upgrading.

## First launch

Choose **DietApp Cloud** to open the hosted web app (Google sign-in works there),
or **My server** and enter the server URL, e.g. `http://192.168.1.100:8080`. Find your
server machine's LAN IP (`ip addr` on Linux, `ipconfig` on Windows) and make
sure the phone is on the same network.

The URL is stored in SharedPreferences. To change it later, clear app data
(Settings → Apps → DietApp → Storage → Clear data) and relaunch.

## Ingredient scanning on Android

Open **Scan** in the nav. Tapping "Take photo" or "Choose image" opens the
native camera/gallery picker. The photo is uploaded to your server, the vision
model extracts ingredients, and anything on your blacklist is flagged in red.

If the photo shows a **barcode**, the digits printed under the bars are read
too. The server looks the product up on Open Food Facts and shows its
ingredient list, allergens and a fit verdict against your restrictions and
blacklist — falling back to the label-read ingredient list when the code
isn't in the database.

Requires a vision-capable model configured on the server (a local vision model
via /models, or a cloud vision model like gpt-4o via /profile).
