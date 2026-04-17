package com.telemost.client

import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import mobile.Mobile

/**
 * VPN service that creates a TUN interface and routes all device traffic
 * through the olcRTC SOCKS5 tunnel via tun2socks.
 *
 * Flow: Apps → TUN → tun2socks (Go) → SOCKS5 (127.0.0.1:1080) → VP8 tunnel → VPS → Internet
 */
class TunnelVpnService : VpnService() {

    companion object {
        const val ACTION_START = "com.telemost.client.VPN_START"
        const val ACTION_STOP = "com.telemost.client.VPN_STOP"
        const val EXTRA_SOCKS_PORT = "socks_port"
        private const val TAG = "TunnelVpn"
        private var instance: TunnelVpnService? = null
        var logCallback: ((String) -> Unit)? = null

        fun isRunning(): Boolean = instance != null

        private fun log(msg: String) {
            Log.i(TAG, msg)
            logCallback?.invoke("[VPN] $msg")
        }
        private fun logErr(msg: String) {
            Log.e(TAG, msg)
            logCallback?.invoke("[VPN] ERROR: $msg")
        }
    }

    private var tunFd: ParcelFileDescriptor? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
        // Required for Android 14+: VPN service must run as foreground
        val channelId = "olcrtc_vpn"
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {
            val channel = android.app.NotificationChannel(
                channelId, "olcRTC VPN",
                android.app.NotificationManager.IMPORTANCE_LOW
            )
            (getSystemService(NOTIFICATION_SERVICE) as android.app.NotificationManager)
                .createNotificationChannel(channel)
        }
        val notification = if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {
            android.app.Notification.Builder(this, channelId)
        } else {
            @Suppress("DEPRECATION") android.app.Notification.Builder(this)
        }.setContentTitle("olcRTC VPN")
            .setContentText("Routing all traffic through tunnel")
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setOngoing(true)
            .build()
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(3, notification,
                android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(3, notification)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                stopVpn()
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_START -> {
                val socksPort = intent.getIntExtra(EXTRA_SOCKS_PORT, 1080)
                startVpn(socksPort)
            }
        }
        return START_STICKY
    }

    private fun startVpn(socksPort: Int) {
        log("Starting VPN, SOCKS port=$socksPort")

        // Create TUN interface
        val builder = Builder()
            .setSession("olcRTC Tunnel")
            .addAddress("10.0.0.2", 24)
            .addRoute("0.0.0.0", 0)
            .addDnsServer("1.1.1.1")
            .addDnsServer("8.8.8.8")
            .setMtu(1500)
            .setBlocking(true)

        // Exclude our own app from VPN to prevent routing loops
        try {
            builder.addDisallowedApplication(packageName)
        } catch (e: Exception) {
            log("Could not exclude own app: ${e.message}")
        }

        tunFd = builder.establish()
        if (tunFd == null) {
            logErr("Failed to establish VPN — builder.establish() returned null")
            stopSelf()
            return
        }

        val fd = tunFd!!.fd
        log("VPN established, TUN fd=$fd")

        // Note: Do NOT call Mobile.setProtector() here — it would overwrite
        // the protector used by the main tunnel, causing a routing loop.
        // The main tunnel's protector (set during Mobile.start()) already
        // protects WebRTC/SOCKS sockets from VPN routing.

        // Start tun2socks in Go — routes TUN packets through SOCKS5
        Thread {
            try {
                log("Starting tun2socks fd=$fd socks=127.0.0.1:$socksPort")
                // Do NOT call Mobile.setLogWriter() here — it would replace
                // the controller's log writer and break Disk log upload.
                Mobile.startTun(fd.toLong(), socksPort.toLong())
                log("tun2socks stopped")
            } catch (e: Exception) {
                logErr("tun2socks error: ${e.message}")
            }
        }.start()
    }

    private fun stopVpn() {
        log("Stopping VPN")
        try {
            Mobile.stopTun()
        } catch (e: Exception) {
            log("stopTun error: ${e.message}")
        }
        tunFd?.close()
        tunFd = null
    }

    override fun onDestroy() {
        stopVpn()
        instance = null
        super.onDestroy()
    }

    override fun onRevoke() {
        stopVpn()
        stopSelf()
    }
}
