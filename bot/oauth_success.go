package main

import (
	"html/template"
	"log"
	"net/http"
)

var oauthSuccessTmpl = template.Must(template.New("oauth").Parse(oauthSuccessHTML))

func writeOAuthSuccess(w http.ResponseWriter, userName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := oauthSuccessTmpl.Execute(w, map[string]string{"UserName": userName}); err != nil {
		log.Printf("oauth success render: %v", err)
	}
}

// oauthSuccessHTML mirrors the Charles Lurch / Itacitrus brand used on
// assistente.itacitrus.com.br: Plus Jakarta Sans, light theme, green #4bb71b
// accent. Confetti animation kept (adapted from the packing_house repo's
// animations/confetti.js — ES `export` stripped to run as a classic script).
const oauthSuccessHTML = `<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Autorizado — Charles Lurch</title>
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Ccircle cx='16' cy='16' r='13' fill='%234bb71b'/%3E%3Ctext x='16' y='21' font-family='system-ui,sans-serif' font-size='14' font-weight='700' text-anchor='middle' fill='white'%3EC%3C/text%3E%3C/svg%3E">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap">
<style>
  :root {
    --ink: #131a15;
    --ink-soft: #3a4640;
    --ink-muted: #6a7671;
    --ink-faint: #97a29d;
    --paper: #ffffff;
    --paper-2: #f6faf4;
    --line: #e3eade;
    --line-soft: #edf1e8;
    --green: #4bb71b;
    --green-dark: #2a8a0e;
  }
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  html, body {
    height: 100%;
    font-family: "Plus Jakarta Sans", system-ui, -apple-system, BlinkMacSystemFont, sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    color: var(--ink);
    background:
      radial-gradient(ellipse at 50% -10%, #e9f5dc 0%, transparent 55%),
      radial-gradient(ellipse at 10% 110%, #f0f9e6 0%, transparent 50%),
      var(--paper);
    background-attachment: fixed;
    overflow: hidden;
  }
  body {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1.25rem;
  }
  .card {
    position: relative;
    z-index: 2;
    background: var(--paper);
    border: 1px solid var(--line);
    border-radius: 22px;
    padding: 2.5rem 2.25rem 2.25rem;
    text-align: center;
    max-width: 460px;
    width: 100%;
    box-shadow:
      0 32px 60px -28px rgba(18, 62, 12, 0.18),
      0 6px 18px rgba(19, 26, 21, 0.05);
    animation: cardIn 0.55s cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes cardIn {
    from { opacity: 0; transform: translateY(14px) scale(0.985); }
    to   { opacity: 1; transform: translateY(0)    scale(1); }
  }
  .brand-mark {
    font-size: 0.88rem;
    color: var(--ink-muted);
    margin-bottom: 1.75rem;
    letter-spacing: -0.005em;
  }
  .brand-mark strong {
    color: var(--ink);
    font-weight: 700;
    font-size: 0.96rem;
    letter-spacing: -0.015em;
    margin-right: 0.3rem;
  }
  .brand-mark strong .dot { color: var(--green); }
  h1 {
    font-size: clamp(1.45rem, 4vw, 1.65rem);
    font-weight: 700;
    letter-spacing: -0.028em;
    line-height: 1.15;
    margin: 1.5rem 0 0.55rem;
    color: var(--ink);
  }
  h1 .green { color: var(--green); }
  .lead {
    font-size: 1rem;
    line-height: 1.55;
    color: var(--ink-soft);
    margin-bottom: 1.5rem;
  }
  .lead .name {
    color: var(--green-dark);
    font-weight: 700;
  }
  .hint {
    display: inline-block;
    font-size: 0.82rem;
    color: var(--ink-faint);
    padding-top: 1.25rem;
    border-top: 1px solid var(--line-soft);
    width: 100%;
  }
  .checkmark {
    width: 82px;
    height: 82px;
    border-radius: 50%;
    display: block;
    stroke-width: 3;
    stroke: var(--green);
    stroke-miterlimit: 10;
    box-shadow: inset 0 0 0 var(--green);
    margin: 0 auto;
    animation:
      fill 0.45s ease-in-out 0.45s forwards,
      scale 0.3s ease-in-out 0.95s both;
  }
  .checkmark__circle {
    stroke-dasharray: 166;
    stroke-dashoffset: 166;
    stroke-width: 2;
    stroke-miterlimit: 10;
    stroke: var(--green);
    fill: transparent;
    animation: stroke 0.65s cubic-bezier(0.65, 0, 0.45, 1) forwards;
  }
  .checkmark__check {
    transform-origin: 50% 50%;
    stroke-dasharray: 48;
    stroke-dashoffset: 48;
    animation: stroke 0.35s cubic-bezier(0.65, 0, 0.45, 1) 0.85s forwards;
  }
  @keyframes stroke { to { stroke-dashoffset: 0; } }
  @keyframes scale {
    0%, 100% { transform: none; }
    50%      { transform: scale3d(1.08, 1.08, 1); }
  }
  @keyframes fill { to { box-shadow: inset 0 0 0 50px rgba(75, 183, 27, 0.1); } }
  @media (max-width: 480px) {
    .card { padding: 2.25rem 1.5rem 1.75rem; }
  }
</style>
</head>
<body>
<div class="card">
  <div class="brand-mark">
    <strong>Charles Lurch<span class="dot">.</span></strong>
    por Itacitrus
  </div>
  <svg class="checkmark" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 52 52" aria-hidden="true">
    <circle class="checkmark__circle" cx="26" cy="26" r="25" fill="none"/>
    <path class="checkmark__check" fill="none" d="M14.1 27.2l7.1 7.2 16.7-16.8"/>
  </svg>
  <h1>Google Calendar conectado<span class="green">.</span></h1>
  <p class="lead">Boa, <span class="name">{{.UserName}}</span>! A sua agenda agora está em boas mãos — o Charles toma conta.</p>
  <p class="hint">Pode fechar esta janela e voltar ao WhatsApp.</p>
</div>
<script>
// confetti animation — adapted from github.com/Itacitrus/packing_house
var confetti = {
  maxCount: 150, speed: 15, frameInterval: 15, alpha: 1.0, gradient: true,
  start: null, stop: null, toggle: null, pause: null, resume: null,
  togglePause: null, remove: null, isPaused: null, isRunning: null
};
(function() {
  confetti.start = startConfetti; confetti.stop = stopConfetti;
  confetti.toggle = toggleConfetti; confetti.pause = pauseConfetti;
  confetti.resume = resumeConfetti; confetti.togglePause = toggleConfettiPause;
  confetti.isPaused = isConfettiPaused; confetti.remove = removeConfetti;
  confetti.isRunning = isConfettiRunning;
  var supportsAnimationFrame = window.requestAnimationFrame || window.webkitRequestAnimationFrame || window.mozRequestAnimationFrame || window.oRequestAnimationFrame || window.msRequestAnimationFrame;
  var colors = ["rgba(75,183,27,","rgba(42,138,14,","rgba(139,195,74,","rgba(233,212,67,","rgba(255,215,0,","rgba(152,251,152,","rgba(173,216,230,","rgba(255,192,203,","rgba(106,90,205,","rgba(244,164,96,","rgba(238,130,238,","rgba(210,105,30,"];
  var streamingConfetti = false, animationTimer = null, pause = false;
  var lastFrameTime = Date.now(), particles = [], waveAngle = 0, context = null;
  function resetParticle(particle, width, height) {
    particle.color = colors[(Math.random() * colors.length) | 0] + (confetti.alpha + ")");
    particle.color2 = colors[(Math.random() * colors.length) | 0] + (confetti.alpha + ")");
    particle.x = Math.random() * width;
    particle.y = Math.random() * height - height;
    particle.diameter = Math.random() * 10 + 5;
    particle.tilt = Math.random() * 10 - 10;
    particle.tiltAngleIncrement = Math.random() * 0.07 + 0.05;
    particle.tiltAngle = Math.random() * Math.PI;
    return particle;
  }
  function toggleConfettiPause() { if (pause) resumeConfetti(); else pauseConfetti(); }
  function isConfettiPaused() { return pause; }
  function pauseConfetti() { pause = true; }
  function resumeConfetti() { pause = false; runAnimation(); }
  function runAnimation() {
    if (pause) return;
    else if (particles.length === 0) {
      context.clearRect(0, 0, window.innerWidth, window.innerHeight);
      animationTimer = null;
    } else {
      var now = Date.now();
      var delta = now - lastFrameTime;
      if (!supportsAnimationFrame || delta > confetti.frameInterval) {
        context.clearRect(0, 0, window.innerWidth, window.innerHeight);
        updateParticles();
        drawParticles(context);
        lastFrameTime = now - (delta % confetti.frameInterval);
      }
      animationTimer = requestAnimationFrame(runAnimation);
    }
  }
  function startConfetti(timeout, min, max) {
    var width = window.innerWidth, height = window.innerHeight;
    window.requestAnimationFrame = (function() {
      return window.requestAnimationFrame || window.webkitRequestAnimationFrame || window.mozRequestAnimationFrame || window.oRequestAnimationFrame || window.msRequestAnimationFrame ||
        function (callback) { return window.setTimeout(callback, confetti.frameInterval); };
    })();
    var canvas = document.getElementById("confetti-canvas");
    if (canvas === null) {
      canvas = document.createElement("canvas");
      canvas.setAttribute("id", "confetti-canvas");
      canvas.setAttribute("style", "display:block;z-index:1;pointer-events:none;position:fixed;top:0;left:0");
      document.body.prepend(canvas);
      canvas.width = width; canvas.height = height;
      window.addEventListener("resize", function() {
        canvas.width = window.innerWidth; canvas.height = window.innerHeight;
      }, true);
      context = canvas.getContext("2d");
    } else if (context === null) context = canvas.getContext("2d");
    var count = confetti.maxCount;
    if (min) {
      if (max) {
        if (min == max) count = particles.length + max;
        else {
          if (min > max) { var temp = min; min = max; max = temp; }
          count = particles.length + ((Math.random() * (max - min) + min) | 0);
        }
      } else count = particles.length + min;
    } else if (max) count = particles.length + max;
    while (particles.length < count) particles.push(resetParticle({}, width, height));
    streamingConfetti = true; pause = false; runAnimation();
    if (timeout) window.setTimeout(stopConfetti, timeout);
  }
  function stopConfetti() { streamingConfetti = false; }
  function removeConfetti() { stopConfetti(); pause = false; particles = []; }
  function toggleConfetti() { if (streamingConfetti) stopConfetti(); else startConfetti(); }
  function isConfettiRunning() { return streamingConfetti; }
  function drawParticles(context) {
    var particle, x, y, x2, y2;
    for (var i = 0; i < particles.length; i++) {
      particle = particles[i];
      context.beginPath();
      context.lineWidth = particle.diameter;
      x2 = particle.x + particle.tilt;
      x = x2 + particle.diameter / 2;
      y2 = particle.y + particle.tilt + particle.diameter / 2;
      if (confetti.gradient) {
        var gradient = context.createLinearGradient(x, particle.y, x2, y2);
        gradient.addColorStop("0", particle.color);
        gradient.addColorStop("1.0", particle.color2);
        context.strokeStyle = gradient;
      } else context.strokeStyle = particle.color;
      context.moveTo(x, particle.y);
      context.lineTo(x2, y2);
      context.stroke();
    }
  }
  function updateParticles() {
    var width = window.innerWidth, height = window.innerHeight, particle;
    waveAngle += 0.01;
    for (var i = 0; i < particles.length; i++) {
      particle = particles[i];
      if (!streamingConfetti && particle.y < -15) particle.y = height + 100;
      else {
        particle.tiltAngle += particle.tiltAngleIncrement;
        particle.x += Math.sin(waveAngle) - 0.5;
        particle.y += (Math.cos(waveAngle) + particle.diameter + confetti.speed) * 0.5;
        particle.tilt = Math.sin(particle.tiltAngle) * 15;
      }
      if (particle.x > width + 20 || particle.x < -20 || particle.y > height) {
        if (streamingConfetti && particles.length <= confetti.maxCount) resetParticle(particle, width, height);
        else { particles.splice(i, 1); i--; }
      }
    }
  }
})();
window.addEventListener("load", function() {
  confetti.start();
  setTimeout(function() { confetti.stop(); }, 2200);
});
</script>
</body>
</html>
`
