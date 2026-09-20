'use strict';
const app = (() => {
  const $ = (s, el=document) => el.querySelector(s);
  const $$ = (s, el=document) => [...el.querySelectorAll(s)];
  const tip = $('#tip');

  function showTip(html, ev){
    if(!tip) return;
    tip.innerHTML = html;
    tip.style.display = 'block';
    const move = e => {
      tip.style.left = (e.clientX+14)+'px';
      tip.style.top = (e.clientY+14)+'px';
    };
    move(ev);
    const hide = () => { tip.style.display='none'; document.removeEventListener('mousemove', move); document.removeEventListener('mouseleave', hide); };
    document.addEventListener('mousemove', move);
    document.addEventListener('mouseleave', hide);
    setTimeout(hide, 6000);
  }
  const fmt = (x, n=4) => (x===null||x===undefined||Number.isNaN(x)) ? '—' : Number(x).toFixed(n);
  const hkl = h => `(${h[0]} ${h[1]} ${h[2]})`;

  async function api(method, url, body){
    const opt = {method, headers:{'Content-Type':'application/json'}};
    if(body !== undefined) opt.body = JSON.stringify(body);
    const res = await fetch(url, opt);
    const txt = await res.text();
    let data; try { data = JSON.parse(txt); } catch { data = {raw: txt}; }
    if(!res.ok) throw new Error(data.error || ('HTTP '+res.status));
    return data;
  }

  function issuesHtml(issues){
    if(!issues || !issues.length) return '<p class="muted">无问题。</p>';
    return issues.map(i => `<div class="issue ${i.code==='bad_wavelength'||i.code==='bad_unit'||i.code==='empty_peaks'||i.code==='out_of_range'||i.code==='bad_d'||i.code==='non_finite'||i.code==='arcsin_domain'?'error':''}">
      <b>[${i.code}]</b> ${i.index!==undefined?('峰#'+i.index+'：'):''}${i.message}</div>`).join('');
  }

  function readForm(){
    const systems = $$('#systems input:checked').map(x => x.value);
    const rules = $$('#rules option:selected').map(x => x.value).filter(r => r !== 'none' && r !== 'P');
    const seed = parseInt($('#seed').value, 10);
    return {
      peak_text: $('#peakText').value,
      unit: $('#unit').value,
      wavelength: parseFloat($('#wavelength').value),
      systems, rules,
      max_volume: parseFloat($('#maxVolume').value),
      seed: Number.isFinite(seed) ? seed : 42,
      top_k: parseInt($('#topK').value,10) || 8,
      random_trials: parseInt($('#randomTrials').value,10) || 150,
      workers: parseInt($('#workers').value,10) || 4,
    };
  }

  function initIndex(){
    $('#loadFixture').onclick = async () => {
      const name = $('#fixture').value;
      if(!name) return;
      const fs = await (await fetch('/api/fixtures')).json();
      // fixtures 列表不带峰，直接提交名称由后端填充并校验回显：改为请求生成不可用，
      // 这里用 /api/jobs 的预校验不可用，因此采用：提交 fixture 后取作业归一化峰。
      $('#submitMsg').textContent = '已选择 '+name+'，点击“提交求解”将由后端生成带噪合成峰。';
    };
    $('#submit').onclick = async () => {
      const f = readForm();
      const name = $('#fixture').value;
      const payload = name ? {fixture: name, unit:f.unit, wavelength:f.wavelength, systems:f.systems, rules:f.rules,
        max_volume:f.max_volume, seed:f.seed, top_k:f.top_k, random_trials:f.random_trials, workers:f.workers}
        : f;
      $('#submitMsg').textContent = '提交中…';
      try{
        const r = await api('POST','/api/jobs', payload);
        location.href = '/jobs/'+r.job_id;
      }catch(e){ $('#submitMsg').textContent = '失败：'+e.message; }
    };
    $('#preview').onclick = async () => {
      // 预校验：提交会真正建作业，改为只做客户端基本检查 + 服务端错误回显（失败时不跳转）。
      const f = readForm();
      if(!f.peak_text.trim()){ alert('请粘贴峰位或选择 fixture'); return; }
      $('#validateCard').style.display='block';
      $('#issues').innerHTML = '<p class="muted">正在服务端校验…</p>';
      try{
        const r = await api('POST','/api/jobs', f);
        $('#issues').innerHTML = `<div class="issue">输入有效，已创建作业 <a href="/jobs/${r.job_id}">${r.job_id}</a>（幂等：重复提交返回同一作业）。</div>`;
      }catch(e){ $('#issues').innerHTML = `<div class="issue error">${e.message}</div>`; }
    };
    loadJobs();
  }

  async function loadJobs(){
    const jobs = await api('GET','/api/jobs');
    const tb = $('#jobs tbody');
    tb.innerHTML = jobs.map(j => `<tr>
      <td><a href="/jobs/${j.id}">${j.id}</a>${j.parent_candidate_id?' <span class="tag amb">派生</span>':''}</td>
      <td><span class="tag ${j.status==='completed'?'good':j.status==='aborted'||j.status==='error'?'bad':'warn'}">${j.status}${j.active?'（运行中）':''}</span></td>
      <td class="mono muted">${j.fingerprint.slice(0,12)}</td>
      <td class="muted">${new Date(j.created_at).toLocaleString()}</td>
      <td><a href="/jobs/${j.id}">打开</a></td>
    </tr>`).join('') || '<tr><td colspan="5" class="muted">暂无作业</td></tr>';
  }

  function initJob(jobID, active){
    $('#configJson').textContent = JSON.stringify(window.__BOOT_CONFIG__ || {}, null, 2);
    $('#cancelBtn').onclick = async () => {
      await api('POST', `/api/jobs/${jobID}/cancel`, {});
      setTimeout(()=>location.reload(), 400);
    };
    const boot = window.__BOOT_RESULT__;
    if(boot) renderResult(boot);
    let timer = null;
    async function poll(){
      const j = await api('GET','/api/jobs/'+jobID);
      $('#status').textContent = j.status + (j.active?'（运行中）':'');
      $('#status').className = 'tag ' + (j.status==='completed'?'good':j.status==='aborted'||j.status==='error'?'bad':'warn');
      if(j.issues && j.issues.length) $('#issues').innerHTML = issuesHtml(j.issues);
      if(j.result) {
        renderResult(j.result);
        $('#runningHint').style.display = 'none';
        stop();
      }
      if(j.status==='aborted'||j.status==='error'){
        $('#runningHint').textContent = j.status==='aborted' ? '作业已中止：Top-K 未发布（不会出现半成品候选）。可修改输入后重新提交。' : '作业因输入错误未产生候选。';
        stop();
      }
    }
    function stop(){ if(timer){ clearInterval(timer); timer=null; } }
    if(active || (boot && boot.status==='running')){ timer = setInterval(poll, 1000); }
    if(!boot) poll();
  }

  function renderResult(result){
    const cs = result.candidates || [];
    const box = $('#candidates');
    if(!cs.length){ box.innerHTML = '<p class="muted">无候选（可能全部超体积上限或输入无效）。</p>'; return; }
    box.innerHTML = cs.map(c => {
      const s = c.score;
      const cell = c.reduced_cell;
      return `<a class="candlink" href="/candidates/${getJobID()}/${c.id}">
        <div class="row">
          <b>#${c.rank} · ${cell.system}</b>
          <span class="tag">${c.source}</span>
          ${c.locked?'<span class="tag good">已锁定</span>':''}
          ${s.ambiguous_count?`<span class="tag amb">歧义峰 ${s.ambiguous_count}</span>`:''}
          ${s.unexplained?`<span class="tag bad">未解释 ${s.unexplained}</span>`:''}
          ${s.missing?`<span class="tag warn">缺失 ${s.missing}</span>`:''}
          <span style="flex:1"></span>
          <b class="mono">score ${fmt(s.score,3)}</b>
        </div>
        <div class="muted mono" style="font-size:12px;margin-top:4px">
          a=${fmt(cell.a)} b=${fmt(cell.b)} c=${fmt(cell.c)} Å ·
          α=${fmt(cell.alpha,2)} β=${fmt(cell.beta,2)} γ=${fmt(cell.gamma,2)}° ·
          V=${fmt(c.volume,1)} Å³ ·
          解释 ${s.explained}/${s.observed_used} · RMS z=${fmt(s.rms,3)} · max|z|=${fmt(s.max_abs_residual,2)}
        </div>
        <div class="barwrap" style="margin-top:6px"><div class="bar" style="width:${Math.round(100*s.coverage_term)}%"></div></div>
      </a>`;
    }).join('');
  }

  let __jobID = null;
  function getJobID(){
    if(__jobID) return __jobID;
    const m = location.pathname.match(/^\/jobs\/([^/]+)/);
    return m ? m[1] : '';
  }
  window.setJobID = id => { __jobID = id; };

  function initCandidate(){
    const raw = JSON.parse($('#candidate-data').textContent);
    const jobID = window.__JOB_ID__, candID = window.__CAND_ID__;
    const overrides = window.__OVERRIDES__ || {};
    $('#ver').textContent = raw.algorithm_version + ' · 规则 ' + ((raw.rules||[]).join('+')||'none');

    const cell = raw.reduced_cell;
    const rows = [
      ['晶系', cell.system], ['标准形 a (Å)', fmt(cell.a)], ['b (Å)', fmt(cell.b)], ['c (Å)', fmt(cell.c)],
      ['α (°)', fmt(cell.alpha,2)], ['β (°)', fmt(cell.beta,2)], ['γ (°)', fmt(cell.gamma,2)],
      ['体积 (Å³)', fmt(raw.volume,2)], ['去重键', '<span class="mono" style="font-size:11px">'+raw.canonical_key+'</span>'],
      ['来源', raw.source + (raw.parent_id?('，派生自 '+raw.parent_id):'')],
    ];
    $('#cellkv').innerHTML = rows.map(([k,v]) => `<dt>${k}</dt><dd>${v}</dd>`).join('');
    const s = raw.score;
    $('#scorekv').innerHTML = [
      ['综合 score', fmt(s.score,4)], ['解释峰', `${s.explained} / ${s.observed_used}`],
      ['歧义峰', s.ambiguous_count], ['未解释', s.unexplained], ['预测缺失', s.missing],
      ['RMS 归一化残差', fmt(s.rms,4)], ['最大 |z|', fmt(s.max_abs_residual,3)],
      ['拟合项 (越低越好)', fmt(s.fit_term,4)], ['覆盖率', fmt(s.coverage_term,3)],
      ['系统自由度罚分', fmt(s.parsimony_term,1)],
    ].map(([k,v]) => `<dt>${k}</dt><dd>${v}</dd>`).join('');

    // 杆图：预测反射（细灰/红）+ 观测峰（粗绿）
    drawSticks(raw);
    fillTables(raw, jobID, overrides);

    $('#lockBtn').onclick = async () => {
      const locked = $('#lockBtn').textContent.includes('解除');
      const r = await api('POST', `/api/candidates/${jobID}/${candID}/lock`, {locked: !locked, note: ''});
      $('#lockBtn').textContent = r.locked ? '解除锁定' : '锁定候选';
      $('#lockMsg').textContent = r.locked ? '已锁定（冻结内容不变，仅加标记）。' : '已解除锁定。';
    };
    if(raw.locked) $('#lockBtn').textContent = '解除锁定';
    $('#deriveBtn').onclick = async () => {
      const r = await api('POST', `/api/candidates/${jobID}/${candID}/derive`, {overrides:{}, systems:[]});
      location.href = '/jobs/'+r.job_id;
    };
  }

  function drawSticks(cand){
    const s = cand.score;
    const matchedPeak = new Map();
    (s.matches||[]).forEach(m => matchedPeak.set(m.peak_index, m));
    // 观测峰来自 job result.normalized_peaks 不一定可得；用 matches+unexplained 组装
    const obs = [];
    (s.matches||[]).forEach(m => obs.push({i:m.peak_index, d:m.obs_d, tt:m.obs_two_theta, kind: m.ambiguous?'amb':'ok'}));
    (s.unexplained_peaks||[]).forEach(u => obs.push({i:u.peak_index, d:u.obs_d, tt:u.obs_two_theta, kind:'unx'}));
    const miss = (s.missing_reflections||[]).map(m => ({d:m.pred_d, tt:m.pred_two_theta}));
    // 以 2θ 为横轴；NaN 时退回 d 轴反向。
    const useTT = obs.some(o => Number.isFinite(o.tt)) || miss.some(m => Number.isFinite(m.tt));
    const xOf = o => useTT ? (Number.isFinite(o.tt)?o.tt:null) : o.d;
    const missX = m => useTT ? (Number.isFinite(m.tt)?m.tt:null) : m.d;
    let xs = obs.map(xOf).filter(Number.isFinite).concat(miss.map(missX).filter(Number.isFinite));
    if(!xs.length){ $('#stickPlot').innerHTML = '<p class="muted">无可绘制峰。</p>'; return; }
    let xmin = Math.min(...xs), xmax = Math.max(...xs);
    const pad = (xmax-xmin)*0.05 || 1; xmin -= pad; xmax += pad;
    const W=1060, H=210, ml=46, mr=12, mt=14, mb=30;
    const X = x => ml + (x-xmin)/(xmax-xmin)*(W-ml-mr);
    const color = {ok:'#46d18b', amb:'#c792ea', unx:'#f06b6b'};
    let svg = `<svg viewBox="0 0 ${W} ${H}" style="width:100%">`;
    // 轴
    svg += `<line x1="${ml}" y1="${H-mb}" x2="${W-mr}" y2="${H-mb}" stroke="#2b3947"/>`;
    for(let t=0;t<=5;t++){
      const xv = xmin + (xmax-xmin)*t/5, xx = X(xv);
      svg += `<line x1="${xx}" y1="${H-mb}" x2="${xx}" y2="${H-mb+4}" stroke="#2b3947"/>
        <text x="${xx}" y="${H-mb+17}" fill="#8fa3b5" font-size="10" text-anchor="middle">${fmt(xv,1)}</text>`;
    }
    svg += `<text x="${ml}" y="12" fill="#8fa3b5" font-size="11">${useTT?'2θ (°)':'d (Å)'}</text>`;
    // 缺失预测（下层红线）
    miss.forEach(m => {
      const x = missX(m); if(!Number.isFinite(x)) return;
      svg += `<line x1="${X(x)}" y1="${mt+28}" x2="${X(x)}" y2="${H-mb-10}" stroke="#f06b6b" stroke-opacity="0.55" stroke-width="1"><title>缺失预测 d=${fmt(m.d,3)}</title></line>`;
    });
    // 观测（粗杆）
    obs.forEach(o => {
      const x = xOf(o); if(!Number.isFinite(x)) return;
      const col = color[o.kind];
      svg += `<line x1="${X(x)}" y1="${mt+46}" x2="${X(x)}" y2="${H-mb-2}" stroke="${col}" stroke-width="2.4"><title>#${o.i} d=${fmt(o.d,3)} ${o.kind}</title></line>`;
      svg += `<circle cx="${X(x)}" cy="${mt+44}" r="2.6" fill="${col}"/>`;
    });
    svg += '</svg>';
    $('#stickPlot').innerHTML = svg;
  }

  function fillTables(cand, jobID, overrides){
    const tb = $('#matchTable tbody');
    const rows = (cand.score.matches||[]).map(m => {
      const hs = m.hkls.map((h,i) => {
        const primary = h[0]===m.primary_hkl[0]&&h[1]===m.primary_hkl[1]&&h[2]===m.primary_hkl[2];
        const zc = Math.abs(m.residuals[i]);
        const col = zc<1?'#46d18b':zc<2?'#f0b34e':'#f06b6b';
        return `<span style="margin-right:8px;${primary?'font-weight:700':''}" title="d=${fmt(m.pred_d[i],4)} z=${fmt(m.residuals[i],3)}">
          ${hkl(h)} <span style="color:${col}">z=${fmt(m.residuals[i],2)}</span></span>`;
      }).join('');
      const pos = Number.isFinite(m.obs_two_theta) ? `${fmt(m.obs_two_theta,3)}° / d=${fmt(m.obs_d,4)}` : `d=${fmt(m.obs_d,4)}`;
      const ex = !!overrides[m.peak_index];
      return `<tr class="${ex?'excl':''}"><td>${m.peak_index}</td><td class="mono">${pos}</td>
        <td style="white-space:normal">${hs}${m.ambiguous?'<span class="tag amb" style="margin-left:6px">歧义</span>':''}</td>
        <td class="mono">${fmt(m.pred_d[0],4)}</td><td class="mono">${fmt(m.primary_res,3)}</td>
        <td><button class="small ghost" data-peak="${m.peak_index}" data-ex="${ex?0:1}">${ex?'恢复':'排除'}</button></td></tr>`;
    });
    tb.innerHTML = rows.join('') || '<tr><td colspan="6" class="muted">无匹配峰</td></tr>';
    tb.querySelectorAll('button').forEach(b => b.onclick = async () => {
      const peak = parseInt(b.dataset.peak,10), excluded = b.dataset.ex==='1';
      await api('POST', `/api/jobs/${jobID}/overrides`, {peak_index:peak, excluded, rerun:false});
      b.textContent = excluded?'恢复':'排除';
      b.dataset.ex = excluded?0:1;
      b.closest('tr').classList.toggle('excl', excluded);
      const r = confirm('排除标记已保存（原始峰表不变）。\n\n确定：基于该覆盖派生并重算为新作业；\n取消：仅保留覆盖标记。');
      if(r){
        const res = await api('POST', `/api/jobs/${jobID}/overrides`, {peak_index:peak, excluded, rerun:true});
        if(res.derived_job_id) location.href = '/jobs/'+res.derived_job_id;
      }
    });

    const ut = $('#unxTable tbody');
    ut.innerHTML = (cand.score.unexplained_peaks||[]).map(u => `<tr><td>${u.peak_index}</td><td class="mono">${fmt(u.obs_d,4)}</td>
      <td class="mono">${Number.isFinite(u.obs_two_theta)?fmt(u.obs_two_theta,2):'—'}</td><td class="muted">${u.reason}</td></tr>`).join('')
      || '<tr><td colspan="4" class="muted">无</td></tr>';
    $('#unxCount').textContent = cand.score.unexplained || '';

    const mt = $('#missTable tbody');
    mt.innerHTML = (cand.score.missing_reflections||[]).map(m => `<tr>
      <td class="mono">${hkl(m.hkl)}${m.all_hkls&&m.all_hkls.length>1?` <span class="tag amb">${m.all_hkls.length}简并</span>`:''}</td>
      <td class="mono">${fmt(m.pred_d,4)}</td><td class="mono">${Number.isFinite(m.pred_two_theta)?fmt(m.pred_two_theta,2):'—'}</td>
      <td>${m.near_peak>=0?`#${m.near_peak} <span class="muted">(z=${fmt(m.near_res,2)}${m.near_peak_excluded?', 该峰被排除':''})</span>`:'无'}</td></tr>`).join('')
      || '<tr><td colspan="4" class="muted">无</td></tr>';
    $('#missCount').textContent = cand.score.missing || '';
  }

  async function fetchCandidate(ref){
    // ref = jobID/candidateID
    const parts = ref.trim().split('/');
    if(parts.length !== 2) throw new Error('标识格式应为 jobID/candidateID：'+ref);
    const j = await api('GET','/api/jobs/'+parts[0]);
    const list = (j.result && j.result.candidates) || [];
    const cand = list.find(c => c.id === parts[1]);
    if(!c) throw new Error('候选 '+ref+' 不在已发布的 Top-K 中（可能来自已中止作业）');
    return {job:j, cand};
  }

  function initCompare(){
    $$('#sets button[data-ids]').forEach(b => b.onclick = () => {
      $('#ids').value = b.dataset.ids;
    });
    $('#save').onclick = async () => {
      const ids = $('#ids').value.split(/\s+/).map(s=>s.trim()).filter(Boolean);
      $('#msg').textContent = '加载候选中…';
      try{
        const loaded = [];
        for(const ref of ids){
          loaded.push(await fetchCandidate(ref));
        }
        await api('POST','/api/compare', {id:'', label:$('#label').value||'比较集', candidate_ids:ids});
        renderCompare(loaded);
        $('#msg').textContent = '已保存 '+ids.length+' 个候选。';
      }catch(e){ $('#msg').textContent = '失败：'+e.message; }
    };
  }

  function renderCompare(items){
    const metric = (c, k) => c.score[k];
    const keys = [
      ['晶系', c=>c.reduced_cell.system],
      ['a / b / c (Å)', c=>`${fmt(c.reduced_cell.a,3)} / ${fmt(c.reduced_cell.b,3)} / ${fmt(c.reduced_cell.c,3)}`],
      ['α / β / γ (°)', c=>`${fmt(c.reduced_cell.alpha,2)} / ${fmt(c.reduced_cell.beta,2)} / ${fmt(c.reduced_cell.gamma,2)}`],
      ['体积 (Å³)', c=>fmt(c.volume,1)],
      ['score', c=>fmt(c.score.score,4)],
      ['覆盖率', c=>fmt(c.score.coverage_term,3)],
      ['解释峰', c=>`${c.score.explained}/${c.score.observed_used}`],
      ['RMS z', c=>fmt(c.score.rms,4)],
      ['最大 |z|', c=>fmt(c.score.max_abs_residual,3)],
      ['歧义峰', c=>c.score.ambiguous_count],
      ['未解释', c=>c.score.unexplained],
      ['缺失', c=>c.score.missing],
      ['消光规则', c=>(c.rules||[]).join('+')||'none'],
      ['算法版本', c=>c.algorithm_version],
    ];
    let html = '<table><thead><tr><th>指标</th>'+items.map((x,i)=>`<th>候选 ${i+1} <a href="/candidates/${x.job.id}/${x.cand.id}">打开↗</a></th>`).join('')+'</tr></thead><tbody>';
    keys.forEach(([name, fn]) => {
      html += `<tr><th>${name}</th>`+items.map(x=>`<td>${fn(x.cand)}</td>`).join('')+'</tr>';
    });
    html += '</tbody></table>';
    $('#diffTable').innerHTML = html;
  }

  return {initIndex, initJob, initCandidate, initCompare, setJobID};
})();
