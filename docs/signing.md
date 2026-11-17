# 发布签名 / Release signing

Mettings 的公开 DMG 使用固定的自签名代码签名证书，而不是每次构建变化的 ad-hoc 身份。证书私钥只保存在维护者的 macOS 钥匙串中，不进入仓库或发布包。未来版本必须复用同一证书和 `com.mettings.desktop` 标识。

这不是 Apple Developer ID 签名，也没有经过 Apple 公证。首次打开下载的应用仍可能需要在「系统设置 → 隐私与安全性」中选择「仍要打开」，并授权麦克风。固定签名有助于保持更新前后的应用身份，但不能保证 Gatekeeper、麦克风或钥匙串权限永不重新提示。不要关闭 Gatekeeper，不要为了运行应用导入维护者的根证书。

将应用固定安装到 `/Applications/Mettings.app`，退出旧版本后替换。旧的 ad-hoc 版本升级到自签名版本时，身份发生变化，可能需要再次授权。要获得 Apple 认可的下载与升级体验，需要 Developer ID 和公证。

## 维护者 / Maintainers

在「钥匙串访问 → 证书助理 → 创建证书」中创建名为 `Mettings Local Release` 的代码签名证书，身份类型为自签名根证书，证书类型为代码签名。将证书及私钥保存在登录钥匙串中，安全备份，不要每次发布重新生成。只在构建机器上为该证书配置代码签名信任。

```sh
security find-identity -v -p codesigning
METTINGS_SIGN_IDENTITY='Mettings Local Release' ./scripts/release.sh
codesign --verify --deep --strict build/Mettings.app
codesign -d -r- build/Mettings.app
```

`release.sh` builds the app, signs its nested native helpers and libraries, verifies the bundle, then creates a DMG and SHA-256 checksum. It refuses ad-hoc signing. Ordinary `build.sh` development builds still use ad-hoc signing unless `METTINGS_SIGN_IDENTITY` is set.

The public DMG uses a persistent self-signed certificate, not Apple Developer ID or notarization. Keep the same certificate, private key, bundle identifier and install location across releases. This preserves signing identity but does **not** guarantee that Gatekeeper, microphone or Keychain prompts disappear. Moving from an old ad-hoc build may require permission again. Users should never disable Gatekeeper or install the maintainer's root certificate. For Apple-recognized distribution, use Developer ID signing and notarization instead.

If the identity lives in a dedicated Keychain rather than the default search list, set `METTINGS_SIGN_KEYCHAIN` to its path and unlock it before building. Keep that Keychain and its password outside the repository, with access restricted to the maintainer.
