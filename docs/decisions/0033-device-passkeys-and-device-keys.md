# 0033 每台设备用自己的 Passkey 解锁；不支持 PRF 的设备用设备密钥

- Status: Accepted
- Date: 2026-11-13

## Context

0030 让 Passkey 的 PRF 输出解开身份密钥。0.3.0-alpha1 解锁时列出所有登记过的 Passkey，结果只在一台设备（通常是手机）上创建过 Passkey 时，Mac 和 Windows 的浏览器只能弹“用手机扫码”。另外，Windows Hello 大多数版本不支持 PRF，Windows 根本没法用 PIN 解锁。用户希望每台设备用本机的方式：iPhone 用 Face ID，Android 用指纹，Mac 用触控 ID 或开机密码，Windows 用 PIN。

## Decision

1. **每台设备一把本机 Passkey。** 创建时要求本机认证器（`authenticatorAttachment: platform`、`hints: client-device`）。设备在 localStorage 里记住自己那把的 ID，解锁和关键操作只请求它，浏览器就不会再给出手机选项。首次在新设备上添加时，要用已有的 Passkey（通常是手机）解锁一次。
2. **有 PRF 的设备不变**：PRF 输出包装身份，和 0030 一样。
3. **没有 PRF 的设备用设备密钥**：浏览器里生成不可导出的 AES 密钥（存在 IndexedDB），用它加密身份；密文存在中继上（`devwrap`），中继核对这把 Passkey 的新鲜断言（中继给的一次性挑战，要求 UV）之后才交出。
4. **关键操作对所有登记的 Passkey 都有效**，包括没有 PRF 的那些：关键操作本来只需要签名。
5. 0.2.x 的 Passkey 不能沿用：它们绑定的是旧 Hub 的域名，而且只用来登录，没有 PRF。

## Alternatives

- **只在页面里用 Windows Hello 做界面上的门**（设备密钥存在浏览器里，不经过中继）：偷到浏览器数据就能解锁，Windows Hello 只是摆设。
- **主密码**：0030 已经否决，多一个要记的秘密。
- **等 Windows 普遍支持 PRF**：Windows 11 25H2 和较新的浏览器已经开始支持，但版本不一，用户现在就要用。支持的设备会自动走 PRF。

## Consequences

- 设备密钥这档比 PRF 弱：要同时拿到这台设备的浏览器数据和中继（或者这台设备的 Windows Hello）才能解开身份。只有中继，没有设备密钥；只有浏览器数据，没有 TPM 里的 Windows Hello 密钥，中继不放行。信任页上会标出“设备密钥”。
- 清除浏览器数据会丢掉设备密钥，要用别的设备解锁后重新添加这台设备。
- 中继多了 `devwrap.challenge`、`devwrap.get`（匿名）和 `devwrap.put`（会话）三种请求，`docs/api/core-relay.md`。
