// Suno import stays in the local player. No Suno login or official credits.
(() => {
  let busy = false;
  let controller;
  window.openSunoImport = (value = '') => {
    const panel = document.getElementById('suno-import-panel');
    if (!panel) return;
    panel.hidden = false;
    const input = document.getElementById('suno-import-url');
    if (value) input.value = value;
    input.focus();
    panel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  };
  window.startSunoImport = async (value) => {
    if (busy) return;
    window.openSunoImport(value);
    const input = document.getElementById('suno-import-url');
    const status = document.getElementById('suno-import-status');
    const button = document.getElementById('suno-import-submit');
    const cancel = document.getElementById('suno-import-cancel');
    const lyric = document.getElementById('suno-import-lyrics');
    const local = document.getElementById('suno-import-local');
    let parsed;
    try {
      parsed = new URL(input.value.trim());
      if (parsed.protocol !== 'https:' || !['suno.com','www.suno.com'].includes(parsed.hostname) || parsed.port || parsed.username || parsed.password || !/^\/(song|s)\/[^/]+\/?$/.test(parsed.pathname)) throw new Error();
    } catch (_) {
      status.textContent = '请粘贴 Suno 单首歌曲链接（song 或 s 分享链接）。'; return;
    }
    busy = true; button.disabled = true; input.disabled = true; cancel.hidden = false;
    local.hidden = true; lyric.hidden = true;
    status.textContent = '正在通过 UseSuno 解析、下载并检查音频，请保持此页打开…';
    controller = new AbortController();
    try {
      const response = await fetch(window.API_ROOT + '/suno/import', {
        method: 'POST', headers: {'Content-Type':'application/json','X-Requested-With':'XMLHttpRequest'},
        body: JSON.stringify({url: parsed.href}), signal: controller.signal
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || '导入失败，请稍后重试');
      const lyricsNote = result.lyric_kind === 'synced' ? '同步歌词已保存' : result.lyric_kind === 'text' ? '歌词已保存（普通文本，没有同步时间轴）' : '此歌曲来源没有提供歌词';
      status.textContent = `${result.existing ? '已在曲库中' : '导入完成'}：${result.name}。${lyricsNote}。\n保存位置：${result.path}`;
      if (result.lyrics) {
        lyric.hidden = false;
        lyric.querySelector('pre').textContent = result.lyrics;
      }
      local.hidden = false;
      // Refresh the library without replacing this progress panel or playback.
      if (typeof isLocalMusicPageActive === 'function' && isLocalMusicPageActive()) await refreshLocalMusicPageAfterMutation();
      else {
        await fetch(window.API_ROOT + '/local_music?refresh=1');
      }
    } catch (error) {
      status.textContent = error.name === 'AbortError' ? '已停止等待。若文件恰好已保存，再次导入会识别已有歌曲。' : error.message;
    } finally {
      busy = false; button.disabled = false; input.disabled = false; cancel.hidden = true; controller = null;
    }
  };
  document.addEventListener('submit', event => {
    if (event.target.id !== 'suno-import-form') return;
    event.preventDefault(); void window.startSunoImport(document.getElementById('suno-import-url').value);
  });
  document.addEventListener('click', event => {
    if (event.target.closest('#suno-import-cancel')) controller?.abort();
  });
})();
