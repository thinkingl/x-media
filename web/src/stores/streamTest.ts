import { defineStore } from 'pinia'
import { ref } from 'vue'
import flvjs from 'flv.js'

// 流测试全局状态：切 Tab 不销毁播放器/连接。
// WebRTC(RTCPeerConnection) 与 flv.js player 生命周期由 store 管理，
// 组件卸载后仍存活；重新进入测试页时重新 attach 到 video 元素。
export const useStreamTestStore = defineStore('streamTest', () => {
  // ---- WebRTC 状态 ----
  let pc: RTCPeerConnection | null = null
  let statsTimer: number | null = null
  const wrConnecting = ref(false)
  const wrConnected = ref(false)
  const wrStatus = ref('')
  const wrError = ref('')
  const rtpPackets = ref(0)
  const bitrate = ref('')
  const resolution = ref('')
  const frameRate = ref('')
  const wrDuration = ref('')
  let lastBytes = 0
  let lastTime = 0
  let firstFrameTime = 0
  let trackStream: MediaStream | null = null

  // ---- HTTP-FLV 状态 ----
  let flvPlayer: any = null
  let flvTimer: number | null = null
  const flvConnecting = ref(false)
  const flvPlaying = ref(false)
  const flvStatus = ref('')
  const flvError = ref('')
  const flvResolution = ref('')
  const flvDuration = ref('')
  let flvStartTime = 0
  let attachedVideo: HTMLVideoElement | null = null

  // ---- WebRTC 方法 ----
  async function startWebRTC(url: string) {
    stopWebRTC()
    wrConnecting.value = true
    wrStatus.value = 'connecting'
    wrConnected.value = false
    wrError.value = ''
    rtpPackets.value = 0
    bitrate.value = ''
    resolution.value = ''
    frameRate.value = ''
    wrDuration.value = ''
    lastBytes = 0
    lastTime = 0
    firstFrameTime = 0

    try {
      pc = new RTCPeerConnection()
      pc.addTransceiver('video', { direction: 'recvonly' })

      pc.ontrack = (ev) => {
        trackStream = ev.streams[0]
        emitStreamToVideo()
      }

      pc.onconnectionstatechange = () => {
        const st = pc?.connectionState
        wrStatus.value = st || ''
        if (st === 'connected') {
          wrConnected.value = true
          wrConnecting.value = false
          startStatsTimer()
        } else if (st === 'failed' || st === 'disconnected' || st === 'closed') {
          wrConnecting.value = false
          wrConnected.value = false
          if (st === 'failed') wrError.value = 'WebRTC 连接失败（网络/信令问题）'
          stopStatsTimer()
        }
      }

      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)
      const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/sdp' },
        body: offer.sdp,
      })
      if (!resp.ok) {
        const text = await resp.text()
        throw new Error(`WHEP 信令失败 (${resp.status}): ${text.slice(0, 200)}`)
      }
      const answer = await resp.text()
      await pc.setRemoteDescription({ type: 'answer', sdp: answer })
    } catch (e: any) {
      wrConnecting.value = false
      wrStatus.value = 'failed'
      wrError.value = e.message || 'WebRTC 测试失败'
      stopWebRTC()
    }
  }

  // attachWebRTCVideo 将已建立的媒体流绑定到 video 元素（组件重挂载时调用）。
  function attachWebRTCVideo(video: HTMLVideoElement) {
    if (trackStream) {
      video.srcObject = trackStream
    }
  }

  function emitStreamToVideo() {
    // store 不直接持有 video 元素；由组件挂载时 attach。
    // 此处仅记录状态，组件通过 watch 处理。
  }

  function startStatsTimer() {
    lastTime = performance.now()
    lastBytes = 0
    statsTimer = window.setInterval(async () => {
      if (!pc) return
      try {
        const stats = await pc.getStats()
        const now = performance.now()
        stats.forEach((s) => {
          if (s.type === 'inbound-rtp' && s.kind === 'video') {
            rtpPackets.value = s.packetsReceived || 0
            if (lastBytes > 0) {
              const bytesDelta = (s.bytesReceived || 0) - lastBytes
              const dt = (now - lastTime) / 1000
              if (dt > 0) bitrate.value = formatBitrate((bytesDelta * 8) / dt)
            }
            lastBytes = s.bytesReceived || 0
            lastTime = now
            if (!firstFrameTime && s.packetsReceived > 0) firstFrameTime = now
            if (firstFrameTime) wrDuration.value = `${((now - firstFrameTime) / 1000).toFixed(1)} s`
          }
        })
      } catch {}
    }, 1000)
  }

  function stopStatsTimer() {
    if (statsTimer !== null) {
      clearInterval(statsTimer)
      statsTimer = null
    }
  }

  // ---- HTTP-FLV 方法 ----
  async function startFLV(url: string, video?: HTMLVideoElement) {
    stopFLV()
    if (!flvjs.isSupported()) {
      flvError.value = '当前浏览器不支持 FLV 播放 (需要 MSE)'
      flvStatus.value = 'failed'
      return
    }
    flvConnecting.value = true
    flvStatus.value = 'connecting'
    flvPlaying.value = false
    flvError.value = ''
    flvResolution.value = ''
    flvDuration.value = ''
    flvStartTime = 0
    if (video) attachedVideo = video

    try {
      flvPlayer = flvjs.createPlayer(
        { type: 'flv', isLive: true, url },
        { enableStashBuffer: false, autoCleanupSourceBuffer: true },
      )
      flvPlayer.on(flvjs.Events.ERROR, (errType: any, errDetail: any) => {
        flvError.value = `FLV 错误: ${errType} ${errDetail}`
        flvStatus.value = 'failed'
        flvConnecting.value = false
        flvPlaying.value = false
        destroyFLVPlayer()
      })
      if (attachedVideo) {
        flvPlayer.attachMediaElement(attachedVideo)
      }
      await flvPlayer.load()
      if (attachedVideo) {
        await flvPlayer.play()
      }
      flvConnecting.value = false
      flvPlaying.value = true
      flvStatus.value = 'playing'
      flvStartTime = performance.now()
      startFLVTimer()
    } catch (e: any) {
      flvError.value = e.message || 'FLV 播放失败'
      flvStatus.value = 'failed'
      flvConnecting.value = false
      flvPlaying.value = false
      destroyFLVPlayer()
    }
  }

  // attachFLVVideo 绑定 flv.js 到 video 元素（组件挂载/重挂载时调用）。
  function attachFLVVideo(video: HTMLVideoElement) {
    attachedVideo = video
    if (flvPlayer) {
      try {
        flvPlayer.attachMediaElement(video)
        flvPlayer.play()
      } catch {}
    }
  }

  function startFLVTimer() {
    flvTimer = window.setInterval(() => {
      if (attachedVideo && attachedVideo.videoWidth && attachedVideo.videoHeight) {
        flvResolution.value = `${attachedVideo.videoWidth}x${attachedVideo.videoHeight}`
      }
      if (flvStartTime) {
        flvDuration.value = `${((performance.now() - flvStartTime) / 1000).toFixed(1)} s`
      }
    }, 1000)
  }

  function destroyFLVPlayer() {
    if (flvPlayer) {
      try {
        flvPlayer.pause()
        flvPlayer.unload()
        flvPlayer.detachMediaElement()
        flvPlayer.destroy()
      } catch {}
      flvPlayer = null
    }
    flvConnecting.value = false
    flvPlaying.value = false
  }

  // ---- 公共 ----
  function stopWebRTC() {
    stopStatsTimer()
    if (pc) {
      pc.ontrack = null
      pc.onconnectionstatechange = null
      try {
        pc.close()
      } catch {}
      pc = null
    }
    trackStream = null
    wrConnected.value = false
    wrConnecting.value = false
  }

  function stopFLV() {
    if (flvTimer !== null) {
      clearInterval(flvTimer)
      flvTimer = null
    }
    destroyFLVPlayer()
  }

  function formatBitrate(bps: number): string {
    if (bps > 1e6) return `${(bps / 1e6).toFixed(2)} Mbps`
    if (bps > 1e3) return `${(bps / 1e3).toFixed(1)} kbps`
    return `${Math.round(bps)} bps`
  }

  return {
    // 状态
    wrConnecting, wrConnected, wrStatus, wrError,
    rtpPackets, bitrate, resolution, frameRate, wrDuration,
    flvConnecting, flvPlaying, flvStatus, flvError,
    flvResolution, flvDuration,
    // 方法
    startWebRTC, attachWebRTCVideo, stopWebRTC,
    startFLV, attachFLVVideo, stopFLV,
  }
})
