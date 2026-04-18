package com.telemost.client

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Toast
import java.net.HttpURLConnection
import java.net.URL

/**
 * Unified Yandex SSO flow:
 * Phase 1: OAuth consent → get access_token (disk:app_folder scope)
 * Phase 2: Navigate to Telemost → get session cookies (for room creation)
 * Returns both EXTRA_COOKIES and EXTRA_OAUTH_TOKEN to caller.
 */
class YandexLoginActivity : Activity() {

    companion object {
        const val EXTRA_COOKIES = "yandex_cookies"
        const val EXTRA_OAUTH_TOKEN = "oauth_token"
        private const val YANDEX_CLIENT_ID = "466e5098b9254404a57bb50af62a5160"
        // OAuth URL: authorize with token response type for implicit flow (no client_secret needed on device)
        private const val OAUTH_URL = "https://oauth.yandex.ru/authorize" +
            "?response_type=token" +
            "&client_id=$YANDEX_CLIENT_ID" +
            "&force_confirm=yes"
        private const val TELEMOST_URL = "https://telemost.yandex.ru/"
        private const val SUCCESS_HOST = "telemost.yandex.ru"
    }

    private var oauthToken: String? = null
    private var phase = 1 // 1 = OAuth, 2 = cookies

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val webView = WebView(this)
        setContentView(webView)

        val cookieManager = CookieManager.getInstance()
        cookieManager.setAcceptCookie(true)
        cookieManager.setAcceptThirdPartyCookies(webView, true)
        cookieManager.removeAllCookies(null)

        webView.settings.javaScriptEnabled = true
        webView.settings.domStorageEnabled = true
        webView.settings.userAgentString =
            "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0 Mobile Safari/537.36"

        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView?, url: String?): Boolean {
                val currentUrl = url ?: return false

                // Phase 1: Catch OAuth redirect with token in fragment
                // Yandex implicit flow redirects to: https://oauth.yandex.ru/verification_code#access_token=XXX&...
                if (phase == 1 && currentUrl.contains("access_token=")) {
                    val token = extractTokenFromUrl(currentUrl)
                    if (token != null) {
                        oauthToken = token
                        phase = 2
                        Toast.makeText(this@YandexLoginActivity, "OAuth token received", Toast.LENGTH_SHORT).show()
                        // Phase 2: navigate to Telemost to collect cookies
                        // User is already logged in from OAuth flow, so Telemost should load directly
                        view?.loadUrl(TELEMOST_URL)
                        return true
                    }
                }
                return false
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                super.onPageFinished(view, url)
                val currentUrl = url ?: return

                // Phase 1: Also check page URL for token (some flows put it in the page URL)
                if (phase == 1 && currentUrl.contains("access_token=")) {
                    val token = extractTokenFromUrl(currentUrl)
                    if (token != null) {
                        oauthToken = token
                        phase = 2
                        Toast.makeText(this@YandexLoginActivity, "OAuth token received", Toast.LENGTH_SHORT).show()
                        view?.loadUrl(TELEMOST_URL)
                        return
                    }
                }

                // Phase 2: Collect cookies when we land on Telemost
                if (phase == 2 && currentUrl.contains(SUCCESS_HOST) && !currentUrl.contains("passport.yandex")) {
                    val cookies = extractAllCookies()
                    if (cookies.isNotBlank() && cookies.contains("Session_id")) {
                        Toast.makeText(this@YandexLoginActivity, "Login complete", Toast.LENGTH_SHORT).show()
                        val result = Intent()
                        result.putExtra(EXTRA_COOKIES, cookies)
                        result.putExtra(EXTRA_OAUTH_TOKEN, oauthToken ?: "")
                        setResult(RESULT_OK, result)
                        finish()
                        return
                    }
                }
            }
        }

        // Start Phase 1: OAuth consent
        webView.loadUrl(OAUTH_URL)
    }

    /**
     * Extract access_token from URL fragment or query.
     * Yandex implicit flow puts token in fragment: #access_token=XXX&token_type=bearer&...
     */
    private fun extractTokenFromUrl(url: String): String? {
        // Try fragment first (after #)
        val fragment = url.substringAfter("#", "")
        if (fragment.isNotBlank()) {
            val params = fragment.split("&")
            for (param in params) {
                val parts = param.split("=", limit = 2)
                if (parts.size == 2 && parts[0] == "access_token") {
                    return parts[1]
                }
            }
        }
        // Try query string (after ?)
        val query = url.substringAfter("?", "")
        if (query.isNotBlank()) {
            val params = query.split("&")
            for (param in params) {
                val parts = param.split("=", limit = 2)
                if (parts.size == 2 && parts[0] == "access_token") {
                    return parts[1]
                }
            }
        }
        return null
    }

    private fun extractAllCookies(): String {
        val cookieManager = CookieManager.getInstance()
        val domains = listOf(
            "https://passport.yandex.ru",
            "https://telemost.yandex.ru",
            "https://yandex.ru",
            "https://cloud-api.yandex.ru"
        )
        val cookieMap = mutableMapOf<String, String>()
        for (domain in domains) {
            val raw = cookieManager.getCookie(domain) ?: continue
            for (pair in raw.split(";")) {
                val trimmed = pair.trim()
                val eq = trimmed.indexOf('=')
                if (eq > 0) {
                    cookieMap[trimmed.substring(0, eq)] = trimmed.substring(eq + 1)
                }
            }
        }
        return cookieMap.entries.joinToString("; ") { "${it.key}=${it.value}" }
    }
}
