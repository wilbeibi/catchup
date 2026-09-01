// Keep the short /install alias in the same aggregate counter as /install.sh.
export function onRequest(context) {
  context.env.INSTALL_EVENTS.writeDataPoint({
    indexes: ['installer_start'],
    blobs: [],
    doubles: [1],
  });

  const url = new URL(context.request.url);
  url.pathname = '/install.sh';
  return context.env.ASSETS.fetch(new Request(url, context.request));
}
