# gomobile Android 集成（Go 客户端 + Android 系统 DNS）

本目录通过 [gomobile bind](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile) 将设备端 Go 客户端打成 **AAR**，由 Java/Kotlin 实现 `HostResolver`，在连接服务端前用 **Android 原生解析**（`InetAddress` / `DnsResolver`），避免仅用 Go 运行时解析与系统 DNS 不一致的问题。

## 前置条件

- Go 1.22+
- Android SDK（`ANDROID_HOME`）
- NDK（`ANDROID_NDK_HOME`，或由脚本从 `$ANDROID_HOME/ndk/<version>` 推断）
- 安装工具：

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
```

## 生成 AAR

在项目根目录执行：

```bash
export ANDROID_HOME=/path/to/Android/sdk
# 可选：export ANDROID_NDK_HOME=...
export JAVAPREFIX=my.socks5.proxy   # 生成 Java 包名前缀
./scripts/gomobile-bind-android.sh ./mobile/build/deviceclient.aar
```

将 `deviceclient.aar` 放入 Android 工程的 `app/libs/`，在 `app/build.gradle` 中：

```gradle
dependencies {
    implementation files('libs/deviceclient.aar')
}
```

（若使用 `flatDir` 或其它方式引入 AAR，按你项目惯例即可。）

## API 说明

- **`Mobile.run(cfg, resolver)`**（Go：`Run(cfg, resolver)`）：启动客户端并**阻塞**。参数里**没有** `Context`：标准库的 `context.Context` 无法被 gobind 绑定，若写在导出函数里会导致 **Java 侧根本没有 `run` 方法**。取消方式：在其它线程调用 **`Mobile.stop()`**（例如 `Activity.onDestroy`）。
- **`HostResolver.lookupHost(hostname)`**：返回 **换行分隔的 IP 字符串**（每行一个地址，与 `InetAddress.getHostAddress()` 一致）；因 [gobind 类型限制](https://pkg.go.dev/golang.org/x/mobile/cmd/gobind#hdr-Type_restrictions)，不能从 Java 直接返回 `[]string`，故用 `String` 承载。Go 侧拆成列表后按顺序尝试 TCP 连接，TLS **SNI 仍使用配置里的主机名**（`server_addr` 中的域名）。
- 同一实现也会用于 **SOCKS 转发目标**（CONNECT 里的域名）：在提供 `HostResolver` 时不再用 Go 自带解析器解析这些主机名；仍为 `tcp` / `tcp4` / `tcp6`，其它 `network` 回退为 `DialContext`。

## Kotlin：系统 DNS（InetAddress）

适合大多数场景：走系统 `getaddrinfo`，与 Private DNS 等行为一致（具体行为以 Android 版本为准）。

见 `android/AndroidDns.kt`：在拿到 AAR 后实现生成的 `HostResolver`，把 `lookupHost` 委托给 `AndroidDns.lookupHostLines(hostname)`（返回换行分隔的 IP）。

```kotlin
// 伪代码：在后台线程调用；退出界面时 Mobile.stop()
Mobile.run(cfg, object : HostResolver {
    override fun lookupHost(hostname: String) = AndroidDns.lookupHostLines(hostname)
})
```

`ClientConfig` 在 Java/Kotlin 中由 gomobile 生成（如 `ClientConfig`），字段使用生成的 setter，例如 `setDeviceID`、`setServerAddr`、`setHeartbeatIntervalNs` 等。

若必须使用 `android.net.DnsResolver`（API 29+），在回调里收集 IP 字符串，拼成 **换行分隔的一个 `String`** 再返回；注意在后台线程调用并处理异步完成。

## 在后台线程运行

`Mobile.run` 会阻塞；请在 **后台线程** 调用（如 `Dispatchers.IO` + `SupervisorJob`）。在 **`onDestroy`**（或对应生命周期）里调用 **`Mobile.stop()`** 结束阻塞，避免主线程网络异常。

## 日志

Go 侧仍向 `stderr` 打日志；在 Android 上如需 Logcat，可用 `Runtime.getRuntime().exec` 重定向或使用 `adb logcat`，或后续在 Go 中接入 `log` 接口封装（未包含在本最小集成中）。
