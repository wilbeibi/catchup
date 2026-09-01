// Count an installer start, then serve the same static script. The event is
// deliberately aggregate-only: no IP, user agent, command output, or ID.
export function onRequest(context) {
  context.env.INSTALL_EVENTS.writeDataPoint({
    indexes: ['installer_start'],
    blobs: [],
    doubles: [1],
  });
  return context.env.ASSETS.fetch(context.request);
}
