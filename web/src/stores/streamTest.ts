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
  // wrGeneration 递增使过期异步流程失效：快速两次 start/stop 时，
  // 挂起的 await（createOffer/fetch/setRemoteDescription）不得再动
  // 新连接的状态（否则 SDP 交叉污染到新的 RTCPeerConnection）。
  let wrGeneration = 0
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
  // flvGeneration 使过期的 load()/错误回调失效：旧 player 的迟到事件
  // 不得销毁新建的 player（此前 ERROR 回调直接 destroyFLVPlayer()）。
  let flvGeneration = 0
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
    const gen = wrGeneration
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
      // 局部引用：后续所有 await/回调只用 conn，杜绝共享 pc 变量导致的
      // 过期流程写入新连接。
      const conn = new RTCPeerConnection()
      pc = conn
      conn.addTransceiver('video', { direction: 'recvonly' })

      conn.ontrack = (ev) => {
        if (gen !== wrGeneration) return
        trackStream = ev.streams[0]
      }

      conn.onconnectionstatechange = () => {
        if (gen !== wrGeneration) return
        const st = conn.connectionState
        wrStatus.value = st || ''
        if (st === 'connected') {
          wrConnected.value = true
          wrConnecting.value = false
          startStatsTimer(conn)
        } else if (st === 'failed' || st === 'disconnected' || st === 'closed') {
          wrConnecting.value = false
          wrConnected.value = false
          if (st === 'failed') wrError.value = 'WebRTC 连接失败（网络/信令问题）'
          stopStatsTimer()
        }
      }

      const offer = await conn.createOffer()
      await conn.setLocalDescription(offer)
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
      if (gen !== wrGeneration) return // 已被新的 start/stop 取代
      await conn.setRemoteDescription({ type: 'answer', sdp: answer })
    } catch (e: any) {
      if (gen !== wrGeneration) return // 过期流程不触碰新连接状态
      wrConnecting.value = false
      wrStatus.value = 'failed'
      wrError.value = e.message || 'WebRTC 测试失败'
      stopWebRTC()
    }
  }

  // attachWebRTCVideo 将已建立的媒体流绑定到 video 元素并恢复播放
  // （组件重挂载/keep-alive 重新激活时调用，video 从文档摘除后浏览器会暂停）。
  function attachWebRTCVideo(video: HTMLVideoElement) {
    if (trackStream) {
      video.srcObject = trackStream
      try {
        video.play()
      } catch {}
    }
  }

  function startStatsTimer(conn: RTCPeerConnection) {
    stopStatsTimer() // 防止断线重连叠加多个 interval
    lastTime = performance.now()
    lastBytes = 0
    statsTimer = window.setInterval(async () => {
      if (pc !== conn) return // 连接已替换，停止统计
      try {
        const stats = await conn.getStats()
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
            if (s.frameWidth && s.frameHeight) {
              resolution.value = `${s.frameWidth}x${s.frameHeight}`
            }
            if (s.framesPerSecond) {
              frameRate.value = Number(s.framesPerSecond).toFixed(1)
            }
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
    const gen = flvGeneration
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

    let player: any = null
    try {
      player = flvjs.createPlayer(
        { type: 'flv', isLive: true, url },
        { enableStashBuffer: false, autoCleanupSourceBuffer: true },
      )
      flvPlayer = player
      player.on(flvjs.Events.ERROR, (errType: any, errDetail: any) => {
        if (gen !== flvGeneration || flvPlayer !== player) return // 旧 player 的迟到错误
        flvError.value = `FLV 错误: ${errType} ${errDetail}`
        flvStatus.value = 'failed'
        flvConnecting.value = false
        flvPlaying.value = false
        destroyFLVPlayer()
      })
      if (attachedVideo) {
        player.attachMediaElement(attachedVideo)
      }
      await player.load()
      if (gen !== flvGeneration || flvPlayer !== player) return // 已被取代
      // 先标记播放中让 video 元素可见（v-show），再 play()：
      // play() 的 promise 在 MSE 缓冲出数据后才 resolve，若元素处于
      // display:none 或 src 被重置，Chrome 会以 "play() interrupted by pause()" 拒绝。
      flvConnecting.value = false
      flvPlaying.value = true
      flvStatus.value = 'playing'
      flvStartTime = performance.now()
      startFLVTimer()
      if (attachedVideo) {
        try {
          await attachedVideo.play()
        } catch (e) {
          // 被自动暂停打断（autoplay 策略）：video 已 muted+playsinline，
          // 稍后缓冲出数据时浏览器会自动开始播放，不视为失败。
          setTimeout(() => {
            if (gen === flvGeneration && flvPlayer === player) {
              try {
                player.play()
              } catch {}
            }
          }, 150)
        }
      }
    } catch (e: any) {
      if (gen !== flvGeneration) return // 过期流程不触碰新 player 状态
      flvError.value = e.message || 'FLV 播放失败'
      flvStatus.value = 'failed'
      flvConnecting.value = false
      flvPlaying.value = false
      if (flvPlayer === player) {
        destroyFLVPlayer()
      }
    }
  }

  // attachFLVVideo 绑定 flv.js 到 video 元素（组件挂载/重挂载时调用）。
  // 幂等：已绑定同一元素时仅确保播放中（startFLV 后 watch 会重复触发，
  // 重复 attachMediaElement 会重建 MSE 源导致 play() 被 pause() 打断）。
  function attachFLVVideo(video: HTMLVideoElement) {
    if (attachedVideo === video) {
      if (flvPlayer && video.paused) {
        try {
          flvPlayer.play()
        } catch {}
      }
      return
    }
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
    wrGeneration++ // 使挂起的信令流程全部失效
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
    flvGeneration++ // 使过期的 load()/错误回调失效
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
