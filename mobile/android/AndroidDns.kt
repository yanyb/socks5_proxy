package my.socks5.proxy

import java.net.InetAddress

/**
 * DNS helpers using [InetAddress.getAllByName], i.e. Android/Java system resolver.
 *
 * After you add the gomobile AAR, wire this into the generated [HostResolver] interface, for example:
 *
 * ```
 * object : HostResolver {
 *   override fun lookupHost(hostname: String) = AndroidDns.lookupHostLines(hostname)
 * }
 * ```
 *
 * The exact import for `HostResolver` depends on `-javaprefix` (often `… .mobile.HostResolver`).
 */
object AndroidDns {
    /** One IP per line, for the gomobile [HostResolver] implementation. */
    fun lookupHostLines(hostname: String): String {
        val all = InetAddress.getAllByName(hostname)
        return all.joinToString("\n") { addr ->
            requireNotNull(addr.hostAddress) { "null hostAddress for $hostname" }
        }
    }

    fun lookupHostStrings(hostname: String): Array<String> {
        val all = InetAddress.getAllByName(hostname)
        return Array(all.size) { i ->
            requireNotNull(all[i].hostAddress) { "null hostAddress for $hostname" }
        }
    }
}
