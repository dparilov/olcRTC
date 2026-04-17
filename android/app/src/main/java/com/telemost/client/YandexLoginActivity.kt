package com.telemost.client

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Toast

class YandexLoginActivity : Activity() {

    companion object {
        const val EXTRA_COOKIES = "yandex_cookies"
        private const val LOGIN_URL = "https://passport.yandex.ru/auth?retpath=https%3A%2F%2Ftelemost.yandex.ru%2F"
        private const val SUCCESS_HOST = "telemost.yandex.ru"
    }

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
            override fun onPageFinished(view: WebView?, url: String?) {
                super.onPageFinished(view, url)
                val currentUrl = url ?: return
                if (currentUrl.contains(SUCCESS_HOST) && !currentUrl.contains("passport.yandex")) {
                    val cookies = extractAllCookies()
                    if (cookies.isNotBlank() && cookies.contains("Session_id")) {
                        Toast.makeText(this@YandexLoginActivity, "Login successful", Toast.LENGTH_SHORT).show()
                        val result = Intent()
                        result.putExtra(EXTRA_COOKIES, cookies)
                        setResult(RESULT_OK, result)
                        finish()
                    }
                }
            }
        }

        webView.loadUrl(LOGIN_URL)
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
