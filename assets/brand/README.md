# Jeex Terminal 品牌资源

- `logo-white.png`：用户提供的透明白字标，用于深色背景。
- `logo-black.png`：用户提供的透明黑字标，用于浅色背景。
- `app-icon.png`：此前提供的圆角底应用图标，用于网页及 iOS。
- `platform-icon.png`：最新提供的白底 JX 图标，用于 Windows、飞牛和 Android，按原图比例缩放，不额外加边距或阴影。

原始图片保留不变。安装前端依赖后，在仓库根目录执行 `npm --prefix web run generate:brand`，统一生成网页、桌面、飞牛和移动端资源。仅更新 Windows、飞牛和 Android 时执行 `node web/scripts/brand-assets.mjs desktop fnos android`。页面字标会裁去多余透明留白。iOS 图标移除透明通道，PWA maskable 图标留出裁切安全区。
