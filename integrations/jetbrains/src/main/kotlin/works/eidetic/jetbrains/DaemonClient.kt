// DaemonClient.kt — thin Kotlin HTTP client for the eidetic-daemon JSON API.
//
// Transport selection:
//   - macOS / Linux → Unix-domain socket. Java's built-in java.net.http.HttpClient
//     does NOT expose a hook to plug in a custom SocketChannel, so we implement a
//     minimal raw HTTP/1.1 GET over a SocketChannel.open(UNIX) and parse the
//     response by hand. The wire format we emit is the same shape Node's
//     http.request({ socketPath }) sends, so the daemon's mux accepts it.
//   - Windows / EIDETIC_TCP=1 → use Java's HttpClient over TCP loopback.
//
// The /export endpoint is NDJSON; we don't consume it from the plugin, so we
// keep this client JSON-only.

package works.eidetic.jetbrains

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.Service
import java.net.URI
import java.net.URLEncoder
import java.net.UnixDomainSocketAddress
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.ByteBuffer
import java.nio.channels.SocketChannel
import java.nio.charset.StandardCharsets
import java.time.Duration

/** Engram shape returned by /search, /recent, /engrams. */
data class Engram(
    val id: Long = 0,
    val surface: String = "",
    /** Unix nanoseconds. */
    val ts: Long = 0,
    val payload: String = "",
    val meta: String? = null,
    /** Populated by /search FTS5. */
    val snippet: String? = null,
)

/** /ask response body. */
data class AskResponse(
    val question: String = "",
    val fts_query: String = "",
    val instructions: String = "",
    val engrams: List<Engram> = emptyList(),
)

/** /metrics JSON shape (subset we consume). */
data class MetricsResponse(
    val version: String = "",
    val uptime_seconds: Long = 0,
    val engram_total: Long = 0,
    val engram_by_surface: Map<String, Long> = emptyMap(),
    val db_path: String = "",
    val db_size_bytes: Long = 0,
    val latest_version: String? = null,
    val update_available: Boolean? = null,
)

/** Application-scoped service so all tool windows share one HTTP stack. */
@Service(Service.Level.APP)
class DaemonClient {
    private val mapper = jacksonObjectMapper()

    private val httpClient: HttpClient by lazy {
        HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(5))
            .build()
    }

    private fun settings(): EideticSettings = EideticSettings.getInstance()

    /** macOS/Linux unless `EIDETIC_TCP=1` or running on Windows. */
    private fun useTcp(): Boolean {
        if (settings().forceTcp) return true
        if (System.getenv("EIDETIC_TCP") == "1") return true
        val os = System.getProperty("os.name", "").lowercase()
        return os.contains("win")
    }

    // ── public endpoint wrappers ──────────────────────────────────────────────

    fun healthz(): Map<String, Any?> {
        return mapper.readValue(getRaw("/healthz"))
    }

    fun surfaces(): Map<String, Long> {
        val raw: Map<String, Any?> = mapper.readValue(getRaw("/surfaces"))
        return raw.mapValues { (_, v) -> (v as? Number)?.toLong() ?: 0L }
    }

    fun search(query: String, surface: String? = null, limit: Int = 50): List<Engram> {
        val qs = buildQuery("q" to query, "surface" to surface, "limit" to limit.toString())
        return mapper.readValue(getRaw("/search?$qs"))
    }

    fun recent(limit: Int = 50, surface: String? = null): List<Engram> {
        val qs = buildQuery("limit" to limit.toString(), "surface" to surface)
        return mapper.readValue(getRaw("/recent?$qs"))
    }

    fun ask(question: String, surface: String? = null, limit: Int = 10): AskResponse {
        val qs = buildQuery("question" to question, "surface" to surface, "limit" to limit.toString())
        val body = getRaw("/ask?$qs")
        return mapper.readValue(body)
    }

    fun metrics(): MetricsResponse {
        val body = getRaw("/metrics", headers = mapOf("Accept" to "application/json"))
        return mapper.readValue(body)
    }

    // ── transport ─────────────────────────────────────────────────────────────

    /**
     * Performs a GET against the daemon and returns the response body as a string.
     * Throws DaemonException on non-2xx, timeout, or transport errors.
     */
    private fun getRaw(path: String, headers: Map<String, String> = emptyMap()): String {
        // Note: Long-running network on EDT would freeze the UI. Callers must
        // wrap with ApplicationManager.getApplication().executeOnPooledThread {}.
        check(!ApplicationManager.getApplication().isDispatchThread) {
            "DaemonClient must not be invoked from the EDT"
        }

        return if (useTcp()) getRawTcp(path, headers) else getRawUds(path, headers)
    }

    private fun getRawTcp(path: String, headers: Map<String, String>): String {
        val s = settings()
        val uri = URI.create("http://${s.tcpHost}:${s.tcpPort}$path")
        val builder = HttpRequest.newBuilder(uri)
            .timeout(Duration.ofMillis(s.timeoutMs.toLong()))
            .header("User-Agent", "eidetic-jetbrains/0.0.1")
            .GET()
        headers.forEach { (k, v) -> builder.header(k, v) }

        val resp = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw DaemonException("TCP transport failed: ${e.message}", e)
        }
        if (resp.statusCode() !in 200..299) {
            throw DaemonException("daemon ${resp.statusCode()}: ${resp.body().take(200)}")
        }
        return resp.body()
    }

    /**
     * UDS transport: open SocketChannel.open(UNIX), write raw HTTP/1.1, parse
     * the response. We assume the daemon writes a Content-Length header (the
     * Go net/http server does). Chunked transfer is not exercised by the
     * endpoints this plugin consumes; if that ever changes, add a
     * chunked-body reader here.
     */
    private fun getRawUds(path: String, headers: Map<String, String>): String {
        val s = settings()
        val addr = UnixDomainSocketAddress.of(s.socketPath)
        val req = buildString {
            append("GET ").append(path).append(" HTTP/1.1\r\n")
            append("Host: localhost\r\n")
            append("User-Agent: eidetic-jetbrains/0.0.1\r\n")
            append("Connection: close\r\n")
            headers.forEach { (k, v) -> append(k).append(": ").append(v).append("\r\n") }
            append("\r\n")
        }

        SocketChannel.open(java.net.StandardProtocolFamily.UNIX).use { ch ->
            try {
                ch.connect(addr)
                ch.socket().soTimeout = s.timeoutMs

                val out = ByteBuffer.wrap(req.toByteArray(StandardCharsets.US_ASCII))
                while (out.hasRemaining()) ch.write(out)

                // Drain everything; Connection: close means the daemon ends the
                // stream when done, so we read until EOF.
                val sink = java.io.ByteArrayOutputStream()
                val buf = ByteBuffer.allocate(8192)
                while (true) {
                    buf.clear()
                    val n = ch.read(buf)
                    if (n <= 0) break
                    sink.write(buf.array(), 0, n)
                }
                val raw = sink.toByteArray()
                return parseHttpResponse(raw)
            } catch (e: DaemonException) {
                throw e
            } catch (e: Exception) {
                throw DaemonException("UDS transport failed: ${e.message}", e)
            }
        }
    }

    /**
     * Minimal HTTP/1.1 response parser. Splits headers from body, validates
     * the status line, and returns the body. Good enough for the daemon's
     * fixed-Content-Length JSON responses.
     */
    private fun parseHttpResponse(raw: ByteArray): String {
        // Find the CRLF CRLF that separates headers from body.
        val sep = byteArrayOf(13, 10, 13, 10) // \r\n\r\n
        val idx = indexOfBytes(raw, sep)
        if (idx < 0) throw DaemonException("malformed HTTP response (no header terminator)")

        val headerBytes = raw.copyOfRange(0, idx)
        val bodyBytes = raw.copyOfRange(idx + 4, raw.size)

        val headerText = String(headerBytes, StandardCharsets.US_ASCII)
        val lines = headerText.split("\r\n")
        val statusLine = lines.firstOrNull() ?: throw DaemonException("empty HTTP response")
        // Status line: HTTP/1.1 <code> <reason>
        val parts = statusLine.split(' ', limit = 3)
        if (parts.size < 2) throw DaemonException("malformed status line: $statusLine")
        val code = parts[1].toIntOrNull() ?: throw DaemonException("non-numeric status: ${parts[1]}")

        val body = String(bodyBytes, StandardCharsets.UTF_8)
        if (code !in 200..299) throw DaemonException("daemon $code: ${body.take(200)}")
        return body
    }

    private fun indexOfBytes(haystack: ByteArray, needle: ByteArray): Int {
        if (needle.isEmpty() || haystack.size < needle.size) return -1
        outer@ for (i in 0..haystack.size - needle.size) {
            for (j in needle.indices) {
                if (haystack[i + j] != needle[j]) continue@outer
            }
            return i
        }
        return -1
    }

    private fun buildQuery(vararg params: Pair<String, String?>): String =
        params
            .filter { (_, v) -> !v.isNullOrEmpty() }
            .joinToString("&") { (k, v) ->
                "${URLEncoder.encode(k, "UTF-8")}=${URLEncoder.encode(v, "UTF-8")}"
            }

    companion object {
        @JvmStatic
        fun getInstance(): DaemonClient =
            ApplicationManager.getApplication().getService(DaemonClient::class.java)

        /** Format an engram timestamp (unix-ns → locale string). */
        @JvmStatic
        fun formatEngramTs(tsNanos: Long): String {
            val ms = tsNanos / 1_000_000
            return java.text.SimpleDateFormat("yyyy-MM-dd HH:mm:ss")
                .format(java.util.Date(ms))
        }

        /** Pull a 1-line preview from an engram payload. */
        @JvmStatic
        fun engramPreview(e: Engram, max: Int = 120): String {
            val src = (e.snippet ?: e.payload).replace(Regex("\\s+"), " ").trim()
            return if (src.length > max) src.substring(0, max - 1) + "…" else src
        }
    }
}

/** Thrown for any daemon transport / decoding failure. */
class DaemonException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)
