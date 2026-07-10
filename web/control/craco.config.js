const path = require("path");

module.exports = {
  webpack: {
    // Remote Broker UI Tunnel: emit asset references as relative paths
    // (static/… instead of /static/…) and let lazy chunks auto-detect their
    // publicPath at runtime. Combined with the <base href> the isannd tunnel
    // injects, the SPA renders correctly under a dynamic /node/<id>/ prefix.
    // At the root (/) this is a no-op — relative assets resolve to /static/….
    configure: (webpackConfig) => {
      webpackConfig.output.publicPath = "auto";
      return webpackConfig;
    },
    alias: {
      "@src": path.resolve(__dirname, "src"),
      "@layout": path.resolve(__dirname, "src/layout"),
      "@styles": path.resolve(__dirname, "src/styles"),
      "@pages": path.resolve(__dirname, "src/pages"),
      "@studios": path.resolve(__dirname, "src/studios"),
      "@views": path.resolve(__dirname, "src/views"),
      "@api": path.resolve(__dirname, "src/api"),
      "@components": path.resolve(__dirname, "src/components"),
      "@utils": path.resolve(__dirname, "src/utils"),
      "@hooks": path.resolve(__dirname, "src/hooks"),
      "@config": path.resolve(__dirname, "src/config"),
      "@reducer": path.resolve(__dirname, "src/reducer"),
      "@i18n": path.resolve(__dirname, "src/i18n"),
      "@theme": path.resolve(__dirname, "src/theme"),
    },
  },
};
