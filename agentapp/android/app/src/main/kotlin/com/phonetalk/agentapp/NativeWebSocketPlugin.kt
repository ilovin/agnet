package com.phonetalk.agentapp

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.util.Base64
import android.util.Log
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.webkit.WebViewClient
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodChannel
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.atomic.AtomicLong

class NativeWebSocketPlugin private constructor(
    private val engine: FlutterEngine,
    private val context: Context,
) {
    companion object {
        private const val TAG = "NativeWS"
        private const val METHOD_CHANNEL = "com.phonetalk.agentapp/native_ws"
        private const val EVENT_CHANNEL = "com.phonetalk.agentapp/native_ws_events"
        private var instance: NativeWebSocketPlugin? = null

        fun register(engine: FlutterEngine, context: Context) {
            instance?.cleanup()
            instance = NativeWebSocketPlugin(engine, context)
        }
    }

    private val connections = ConcurrentHashMap<Long, WSConn>()
    private val nextId = AtomicLong(0)
    private var eventSink: EventChannel.EventSink? = null
    private val mainHandler = Handler(Looper.getMainLooper())

    init {
        val self = this
        MethodChannel(engine.dartExecutor.binaryMessenger, METHOD_CHANNEL)
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "connect" -> {
                        val url = call.argument<String>("url") ?: ""
                        val id = nextId.incrementAndGet()
                        val conn = WSConn(id, url)
                        connections[id] = conn
                        conn.connect()
                        result.success(id)
                    }
                    "send" -> {
                        val id = call.argument<Number>("id")?.toLong() ?: -1
                        val message = call.argument<String>("message") ?: ""
                        val conn = connections[id]
                        if (conn != null) {
                            conn.send(message)
                            result.success(null)
                        } else {
                            result.error("NOT_FOUND", "connection $id not found", null)
                        }
                    }
                    "close" -> {
                        val id = call.argument<Number>("id")?.toLong() ?: -1
                        val conn = connections.remove(id)
                        if (conn != null) {
                            conn.close()
                            result.success(null)
                        } else {
                            result.success(null)
                        }
                    }
                    else -> result.notImplemented()
                }
            }

        EventChannel(engine.dartExecutor.binaryMessenger, EVENT_CHANNEL)
            .setStreamHandler(object : EventChannel.StreamHandler {
                override fun onListen(arguments: Any?, sink: EventChannel.EventSink) {
                    eventSink = sink
                    self.drainQueue(sink)
                }
                override fun onCancel(arguments: Any?) {
                    eventSink = null
                }
            })
    }

    private val pendingEvents = ConcurrentLinkedQueue<Map<String, Any?>>()

    private fun sendEvent(event: Map<String, Any?>) {
        mainHandler.post {
            val sink = eventSink
            if (sink != null) {
                try {
                    sink.success(event)
                    Log.d(TAG, "sendEvent delivered: ${event["id"]}/${event["type"]}")
                } catch (e: Exception) {
                    Log.e(TAG, "sendEvent failed: ${e.message}")
                    pendingEvents.add(event)
                }
            } else {
                Log.w(TAG, "sendEvent: sink null, queuing ${event["id"]}/${event["type"]}")
                pendingEvents.add(event)
            }
        }
    }

    private fun drainQueue(sink: EventChannel.EventSink) {
        while (true) {
            val ev = pendingEvents.poll() ?: break
            try { sink.success(ev) } catch (_: Exception) { break }
        }
    }

    private fun sendEvent(connId: Long, type: String, data: String? = null, error: String? = null) {
        val event = mutableMapOf<String, Any?>("id" to connId, "type" to type)
        if (data != null) event["data"] = data
        if (error != null) event["error"] = error
        sendEvent(event)
    }

    private val wsHtml = """<!DOCTYPE html><html><body><script>
var ws=null;
function wsConnect(url){
 ws=new WebSocket(url);
 ws.onopen=function(){NativeBridge.onOpen()};
 ws.onmessage=function(e){
  try{var b=btoa(unescape(encodeURIComponent(e.data)));NativeBridge.onMessage(b)}
  catch(ex){NativeBridge.onError('encode:'+ex.message)}
 };
 ws.onclose=function(e){NativeBridge.onClose(e.code+'')};
 ws.onerror=function(){NativeBridge.onError('ws error')}
}
function wsSend(b64){if(ws){ws.send(decodeURIComponent(escape(atob(b64))))}}
function wsClose(){if(ws)ws.close()}
</script></body></html>"""

    inner class WSConn(private val id: Long, private val url: String) {
        private var webView: WebView? = null
        private var pageLoaded = false

        fun connect() {
            Log.i(TAG, "[$id] connecting (WebView) to $url")
            mainHandler.post {
                val wv = WebView(context)
                wv.settings.javaScriptEnabled = true
                wv.settings.domStorageEnabled = true
                wv.addJavascriptInterface(Bridge(id), "NativeBridge")
                wv.webViewClient = object : WebViewClient() {
                    override fun onPageFinished(view: WebView?, pageUrl: String?) {
                        if (pageLoaded) return
                        pageLoaded = true
                        Log.d(TAG, "[$id] page loaded, initiating WebSocket to $url")
                        val escaped = url.replace("\\", "\\\\").replace("'", "\\'")
                        view?.evaluateJavascript("wsConnect('$escaped')", null)
                    }
                }
                wv.loadDataWithBaseURL("https://localhost/", wsHtml, "text/html", "UTF-8", null)
                webView = wv
            }
        }

        fun send(message: String) {
            val encoded = Base64.encodeToString(message.toByteArray(Charsets.UTF_8), Base64.NO_WRAP)
            mainHandler.post {
                webView?.evaluateJavascript("wsSend('$encoded')", null)
            }
        }

        fun close() {
            mainHandler.post {
                connections.remove(id)
                webView?.evaluateJavascript("wsClose()", null)
                webView?.stopLoading()
                webView?.destroy()
                webView = null
            }
        }
    }

    fun cleanup() {
        for (conn in connections.values) {
            conn.close()
        }
        connections.clear()
        eventSink = null
    }

    inner class Bridge(private val connId: Long) {
        @JavascriptInterface
        fun onOpen() {
            Log.d(TAG, "[$connId] WebSocket opened")
            sendEvent(connId, "open")
        }

        @JavascriptInterface
        fun onMessage(b64Data: String) {
            val data = String(Base64.decode(b64Data, Base64.DEFAULT), Charsets.UTF_8)
            sendEvent(connId, "message", data = data)
        }

        @JavascriptInterface
        fun onClose(code: String) {
            Log.d(TAG, "[$connId] WebSocket closed: $code")
            sendEvent(connId, "closed", data = code)
            connections.remove(connId)
        }

        @JavascriptInterface
        fun onError(error: String) {
            Log.e(TAG, "[$connId] WebSocket error: $error")
            sendEvent(connId, "error", error = error)
            connections.remove(connId)
        }
    }
}
