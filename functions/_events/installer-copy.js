// This route is called only after the Clipboard API confirms the install
// command was copied. It stores an aggregate count, not visitor data.
export function onRequestPost(context) {
  context.env.INSTALL_EVENTS.writeDataPoint({
    indexes: ['installer_copy'],
    blobs: [],
    doubles: [1],
  });
  return new Response(null, {
    status: 204,
    headers: { 'Cache-Control': 'no-store' },
  });
}
