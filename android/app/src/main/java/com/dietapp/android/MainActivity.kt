package com.dietapp.android

import android.app.Activity
import android.app.AlertDialog
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
        private const val FILE_CHOOSER_REQUEST = 1001
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
        webView = findViewById(R.id.webview)

        configureWebView()

        val serverUrl = prefs.getString(KEY_SERVER_URL, null)
        if (serverUrl.isNullOrBlank()) {
            promptForServerUrl()
        } else {
            webView.loadUrl(serverUrl)
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
        if (this::webView.isInitialized && webView.canGoBack()) {
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
