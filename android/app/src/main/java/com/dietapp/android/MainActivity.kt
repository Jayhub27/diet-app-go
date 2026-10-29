package com.dietapp.android

import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.os.Bundle
import android.provider.MediaStore
import android.view.ViewGroup
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.EditText
import androidx.appcompat.app.AppCompatActivity
import androidx.browser.customtabs.CustomTabsIntent
import androidx.core.content.FileProvider
import java.io.File

/**
 * DietApp Android client.
 *
 * A thin WebView wrapper around a self-hosted DietApp server. The server URL
 * is entered on first launch and stored in SharedPreferences. File uploads
 * (including camera capture on the /scan page) are bridged through
 * [WebChromeClient.onShowFileChooser].
 */
class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var prefs: SharedPreferences
    private var filePathCallback: ValueCallback<Array<Uri>>? = null
    private var cameraPhotoUri: Uri? = null

    companion object {
        private const val PREFS_NAME = "dietapp"
        private const val KEY_SERVER_URL = "server_url"
        private const val KEY_MODE = "mode"
        private const val MODE_CLOUD = "cloud"
        private const val MODE_SERVER = "server"
        private const val CLOUD_URL = "https://diet-app-zeta-blond.vercel.app"
        private const val FILE_CHOOSER_REQUEST = 1001
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)

        when (prefs.getString(KEY_MODE, null)) {
            MODE_CLOUD -> showCloudScreen()
            MODE_SERVER -> showServerScreen()
            else -> showModeChooser()
        }
    }

    private fun showModeChooser() {
        AlertDialog.Builder(this)
            .setTitle(getString(R.string.mode_title))
            .setMessage(getString(R.string.mode_message))
            .setCancelable(false)
            .setPositiveButton(getString(R.string.mode_cloud)) { _, _ ->
                prefs.edit().putString(KEY_MODE, MODE_CLOUD).apply()
                showCloudScreen()
            }
            .setNegativeButton(getString(R.string.mode_server)) { _, _ ->
                prefs.edit().putString(KEY_MODE, MODE_SERVER).apply()
                showServerScreen()
            }
            .show()
    }

    private fun showServerScreen() {
        setContentView(R.layout.activity_main)
        webView = findViewById(R.id.webview)
        configureWebView()

        val serverUrl = prefs.getString(KEY_SERVER_URL, null)
        if (serverUrl.isNullOrBlank()) {
            promptForServerUrl()
        } else {
            webView.loadUrl(serverUrl)
        }
    }

    private fun showCloudScreen() {
        setContentView(R.layout.activity_cloud)
        findViewById<android.view.View>(R.id.cloud_open).setOnClickListener { openCloud() }
        findViewById<android.view.View>(R.id.cloud_switch).setOnClickListener {
            prefs.edit().putString(KEY_MODE, MODE_SERVER).apply()
            showServerScreen()
        }
    }

    private fun openCloud() {
        val uri = Uri.parse(CLOUD_URL)
        try {
            CustomTabsIntent.Builder()
                .setShowTitle(true)
                .setUrlBarHidingEnabled(true)
                .build()
                .launchUrl(this, uri)
        } catch (e: ActivityNotFoundException) {
            startActivity(Intent(Intent.ACTION_VIEW, uri))
        }
    }

    private fun configureWebView() {
        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = true
            allowContentAccess = true
            mediaPlaybackRequiresUserGesture = false
            cacheMode = WebSettings.LOAD_DEFAULT
        }

        webView.setOnLongClickListener {
            AlertDialog.Builder(this)
                .setTitle(getString(R.string.switch_title))
                .setItems(arrayOf(getString(R.string.switch_cloud), getString(R.string.switch_server))) { _, which ->
                    when (which) {
                        0 -> {
                            prefs.edit().putString(KEY_MODE, MODE_CLOUD).apply()
                            showCloudScreen()
                        }
                        1 -> promptForServerUrl()
                    }
                }
                .show()
            true
        }

        webView.webViewClient = object : WebViewClient() {
            // Keep all navigation inside the WebView.
        }

        webView.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(
                view: WebView?,
                callback: ValueCallback<Array<Uri>>?,
                params: FileChooserParams?
            ): Boolean {
                filePathCallback?.onReceiveValue(null)
                filePathCallback = callback

                val captureIntent = Intent(MediaStore.ACTION_IMAGE_CAPTURE)
                if (captureIntent.resolveActivity(packageManager) != null) {
                    val photoFile = File(cacheDir, "scan_${System.currentTimeMillis()}.jpg")
                    cameraPhotoUri = FileProvider.getUriForFile(
                        this@MainActivity,
                        "${applicationContext.packageName}.fileprovider",
                        photoFile
                    )
                    captureIntent.putExtra(MediaStore.EXTRA_OUTPUT, cameraPhotoUri)
                }

                val contentIntent = Intent(Intent.ACTION_GET_CONTENT).apply {
                    addCategory(Intent.CATEGORY_OPENABLE)
                    type = "image/*"
                }

                val chooser = Intent(Intent.ACTION_CHOOSER).apply {
                    putExtra(Intent.EXTRA_INTENT, contentIntent)
                    putExtra(Intent.EXTRA_TITLE, "Select or take a photo")
                    putExtra(Intent.EXTRA_INITIAL_INTENTS, arrayOf(captureIntent))
                }
                startActivityForResult(chooser, FILE_CHOOSER_REQUEST)
                return true
            }
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == FILE_CHOOSER_REQUEST) {
            val callback = filePathCallback ?: return
            filePathCallback = null

            if (resultCode != Activity.RESULT_OK) {
                callback.onReceiveValue(null)
                return
            }

            val results: Array<Uri>? = when {
                data?.clipData != null -> {
                    val count = data.clipData!!.itemCount
                    Array(count) { i -> data.clipData!!.getItemAt(i).uri }
                }
                data?.data != null -> arrayOf(data.data!!)
                cameraPhotoUri != null -> arrayOf(cameraPhotoUri!!)
                else -> null
            }
            callback.onReceiveValue(results)
        }
    }

    private fun promptForServerUrl() {
        val input = EditText(this).apply {
            hint = getString(R.string.server_url_hint)
            setText(prefs.getString(KEY_SERVER_URL, "http://"))
            setSelection(text.length)
        }

        val container = android.widget.FrameLayout(this)
        val margin = (20 * resources.displayMetrics.density).toInt()
        val lp = android.widget.FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
        )
        lp.setMargins(margin, 0, margin, 0)
        container.addView(input, lp)

        AlertDialog.Builder(this)
            .setTitle("Connect to your DietApp server")
            .setMessage("Enter the URL of the machine running dietapp (e.g. http://192.168.1.100:8080). It must be reachable from this device.")
            .setView(container)
            .setCancelable(false)
            .setPositiveButton("Connect") { _, _ ->
                var url = input.text.toString().trim()
                if (!url.startsWith("http://") && !url.startsWith("https://")) {
                    url = "http://$url"
                }
                prefs.edit().putString(KEY_SERVER_URL, url).apply()
                webView.loadUrl(url)
            }
            .setNegativeButton("Quit") { _, _ -> finish() }
            .show()
    }

    override fun onBackPressed() {
        val serverMode = prefs.getString(KEY_MODE, null) == MODE_SERVER
        if (serverMode && this::webView.isInitialized && webView.canGoBack()) {
            webView.goBack()
        } else {
            super.onBackPressed()
        }
    }

    override fun onDestroy() {
        if (this::webView.isInitialized) {
            webView.destroy()
        }
        super.onDestroy()
    }
}
