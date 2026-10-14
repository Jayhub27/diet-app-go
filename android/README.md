# DietApp for Android

A thin native Android client for a self-hosted DietApp server.

## What it is

- A WebView wrapper that loads your DietApp server URL
- Native camera + gallery bridge for the ingredient scanner (`/scan`)
- Server URL stored on-device (entered on first launch)
- Back button navigates web history

## Requirements

- Android Studio Hedgehog (2023.1.1) or newer
- Android SDK 24+ (Android 7.0+)
- A running DietApp server reachable from the phone (same Wi-Fi, or a public URL via tunnel/reverse proxy)

## Build

1. Open this `android/` directory in Android Studio.
2. Let Gradle sync (Android Studio will download the wrapper on first sync).
3. Connect a device (USB debugging) or start an emulator.
4. Run ▶ — the app installs and prompts for your server URL.

To build an APK from the command line after first sync:

```bash
./gradlew assembleRelease
# APK lands in app/build/outputs/apk/release/app-release.apk
```

## First launch

The app asks for your server URL, e.g. `http://192.168.1.100:8080`. Find your
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
