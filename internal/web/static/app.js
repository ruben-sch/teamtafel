// Registriert den Service Worker und steuert auf der Einstellungsseite das Push-Abo.
// Ohne JavaScript funktioniert die App weiter, Benachrichtigungen kommen dann per E-Mail.
if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}

const box = document.getElementById('push');
if (box) pushEinrichten(box);

async function pushEinrichten(box) {
  const status = box.querySelector('.status');
  const an = box.querySelector('button.an');
  const aus = box.querySelector('button.aus');
  const standalone = matchMedia('(display-mode: standalone)').matches || navigator.standalone === true;
  if (/iPhone|iPad/.test(navigator.userAgent) && !standalone) {
    box.querySelector('.ios').hidden = false;
  }
  if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) {
    status.textContent = 'Dieser Browser kann hier keine Push-Nachrichten empfangen. Du bekommst E-Mails.';
    return;
  }
  const reg = await navigator.serviceWorker.ready;
  const zeigen = async () => {
    const abo = await reg.pushManager.getSubscription();
    an.hidden = !!abo;
    aus.hidden = !abo;
    if (abo) status.textContent = 'Push ist auf diesem Gerät aktiv.';
    else if (Notification.permission === 'denied') status.textContent = 'Benachrichtigungen sind im Browser blockiert. Du bekommst E-Mails.';
    else status.textContent = 'Push ist auf diesem Gerät aus. Du bekommst E-Mails.';
  };
  const senden = (methode, daten) => fetch('/push/abo', {
    method: methode,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(daten),
  }).then((r) => { if (!r.ok) throw new Error(r.status); });

  an.addEventListener('click', async () => {
    try {
      const abo = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: schluessel(box.dataset.key) });
      await senden('POST', abo);
    } catch (_) {
      status.textContent = 'Aktivieren hat nicht geklappt. Bitte erlaube Benachrichtigungen für diese Seite.';
      return;
    }
    zeigen();
  });
  aus.addEventListener('click', async () => {
    const abo = await reg.pushManager.getSubscription();
    if (abo) {
      await senden('DELETE', { endpoint: abo.endpoint }).catch(() => {});
      await abo.unsubscribe();
    }
    zeigen();
  });
  zeigen();
}

function schluessel(b64) {
  const s = b64.replace(/-/g, '+').replace(/_/g, '/');
  const roh = atob(s + '='.repeat((4 - (s.length % 4)) % 4));
  return Uint8Array.from(roh, (c) => c.charCodeAt(0));
}
