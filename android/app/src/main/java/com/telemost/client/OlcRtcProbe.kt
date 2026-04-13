package com.telemost.client

import mobile.Mobile

object OlcRtcProbe {
    fun probe(): String {
        return buildString {
            append("olcRTC AAR loaded; Mobile class available")
            append(" | version stub: ")
            append(Mobile::class.java.name)
        }
    }
}
