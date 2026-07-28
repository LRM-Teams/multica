const RINGBACK_FREQUENCY_HZ = 450;
const RINGBACK_CYCLE_MS = 5_000;
const RINGBACK_ON_SECONDS = 1;
const RINGBACK_GAIN = 0.055;
const RINGBACK_RAMP_SECONDS = 0.02;

type AudioContextConstructor = typeof AudioContext;

type WindowWithWebkitAudio = Window & typeof globalThis & {
  webkitAudioContext?: AudioContextConstructor;
};

export interface VoiceCallRingback {
  start(): void;
  stop(): void;
}

export type VoiceCallRingbackFactory = () => VoiceCallRingback;

function browserAudioContextConstructor(): AudioContextConstructor | null {
  if (typeof window === "undefined") return null;
  const audioWindow = window as WindowWithWebkitAudio;
  return audioWindow.AudioContext ?? audioWindow.webkitAudioContext ?? null;
}

class BrowserVoiceCallRingback implements VoiceCallRingback {
  private context: AudioContext | null = null;
  private oscillator: OscillatorNode | null = null;
  private gain: GainNode | null = null;
  private cycleTimer: ReturnType<typeof setInterval> | null = null;

  start(): void {
    if (this.context) return;
    const AudioContextClass = browserAudioContextConstructor();
    if (!AudioContextClass) return;

    const context = new AudioContextClass();
    const oscillator = context.createOscillator();
    const gain = context.createGain();
    oscillator.type = "sine";
    oscillator.frequency.value = RINGBACK_FREQUENCY_HZ;
    gain.gain.value = 0;
    oscillator.connect(gain);
    gain.connect(context.destination);

    this.context = context;
    this.oscillator = oscillator;
    this.gain = gain;
    oscillator.start();
    this.schedulePulse();
    this.cycleTimer = setInterval(() => {
      this.schedulePulse();
    }, RINGBACK_CYCLE_MS);

    void context.resume().catch(() => {
      this.stop();
    });
  }

  stop(): void {
    if (this.cycleTimer) {
      clearInterval(this.cycleTimer);
      this.cycleTimer = null;
    }

    const context = this.context;
    const oscillator = this.oscillator;
    const gain = this.gain;
    this.context = null;
    this.oscillator = null;
    this.gain = null;

    if (!context) return;
    const now = context.currentTime;
    gain?.gain.cancelScheduledValues(now);
    gain?.gain.setValueAtTime(0, now);
    try {
      oscillator?.stop();
    } catch {
      // An already-ended oscillator is safe to dispose.
    }
    oscillator?.disconnect();
    gain?.disconnect();
    void context.close().catch(() => undefined);
  }

  private schedulePulse(): void {
    const context = this.context;
    const gain = this.gain;
    if (!context || !gain) return;

    // Chinese PSTN-style outgoing ringback: 450 Hz, one second on and four
    // seconds off. Short ramps avoid audible clicks at each boundary.
    const startsAt = context.currentTime + RINGBACK_RAMP_SECONDS;
    const fullGainAt = startsAt + RINGBACK_RAMP_SECONDS;
    const releaseAt = startsAt + RINGBACK_ON_SECONDS -
      RINGBACK_RAMP_SECONDS;
    const silentAt = startsAt + RINGBACK_ON_SECONDS;
    gain.gain.cancelScheduledValues(startsAt);
    gain.gain.setValueAtTime(0, startsAt);
    gain.gain.linearRampToValueAtTime(RINGBACK_GAIN, fullGainAt);
    gain.gain.setValueAtTime(RINGBACK_GAIN, releaseAt);
    gain.gain.linearRampToValueAtTime(0, silentAt);
  }
}

export function createVoiceCallRingback(): VoiceCallRingback {
  return new BrowserVoiceCallRingback();
}
