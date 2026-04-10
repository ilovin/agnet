// Force reload if cached version detected
if (!window.location.hash.includes('v=2')) {
  window.location.hash = 'v=2';
  window.location.reload(true);
}
