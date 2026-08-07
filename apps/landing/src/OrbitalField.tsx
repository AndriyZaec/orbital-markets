import { useEffect, useRef } from 'react'

const VERTEX_SHADER = `#version 300 es
in vec2 a_position;
void main() { gl_Position = vec4(a_position, 0.0, 1.0); }
`

const FRAGMENT_SHADER = `#version 300 es
precision highp float;

uniform vec2 u_resolution;
uniform float u_time;
uniform sampler2D u_glyphs;
uniform float u_glyph_count;

out vec4 out_color;

float hash(vec2 p) {
  p = fract(p * vec2(123.34, 456.21));
  p += dot(p, p + 45.32);
  return fract(p.x * p.y);
}

float noise(vec2 p) {
  vec2 i = floor(p);
  vec2 f = fract(p);
  f = f * f * (3.0 - 2.0 * f);
  return mix(mix(hash(i), hash(i + vec2(1.0, 0.0)), f.x),
    mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0)), f.x), f.y);
}

float fbm(vec2 p) {
  float value = 0.0;
  float amplitude = 0.5;
  for (int i = 0; i < 5; i++) {
    value += amplitude * noise(p);
    p = mat2(1.7, -1.1, 1.1, 1.7) * p + 0.19;
    amplitude *= 0.5;
  }
  return value;
}

mat2 rotate2d(float angle) {
  float c = cos(angle);
  float s = sin(angle);
  return mat2(c, -s, s, c);
}

void main() {
  float scale = clamp(u_resolution.y / 800.0, 0.72, 1.35);
  vec2 cell_size = vec2(9.0, 15.0) * scale;
  vec2 cell_id = floor(gl_FragCoord.xy / cell_size);
  vec2 cell_center = (cell_id + 0.5) * cell_size;
  vec2 cell_uv = fract(gl_FragCoord.xy / cell_size);
  vec2 p = (cell_center - 0.5 * u_resolution) / u_resolution.y;

  float time = u_time * 0.11;
  float broad_noise = fbm(p * 2.15 + vec2(time, -time * 0.62));
  float detail_noise = fbm(p * 5.8 - vec2(time * 0.4, time * 0.25));

  vec2 body_p = p - vec2(0.22, -0.06);
  float radius = length(body_p);
  float body = smoothstep(0.98, 0.25, radius);
  float body_edge = smoothstep(0.032, 0.003, abs(radius - 0.74 - (broad_noise - 0.5) * 0.045));
  float contour = pow(max(0.0, sin(radius * 28.0 - time * 2.0 + broad_noise * 4.2)), 16.0) * body * 0.32;

  vec2 ring_a_p = rotate2d(-0.26 + sin(time * 0.35) * 0.018) * (p + vec2(0.04, 0.02));
  float ring_a_distance = abs(length(vec2(ring_a_p.x, ring_a_p.y / 0.36)) - 0.91);
  float ring_a = smoothstep(0.023, 0.003, ring_a_distance);

  vec2 ring_b_p = rotate2d(0.38) * (p - vec2(0.08, -0.02));
  float ring_b_distance = abs(length(vec2(ring_b_p.x, ring_b_p.y / 0.59)) - 1.12);
  float ring_b = smoothstep(0.017, 0.003, ring_b_distance) * 0.7;

  float satellite_angle = time * 2.4 + 0.8;
  vec2 satellite_orbit = vec2(cos(satellite_angle) * 0.91, sin(satellite_angle) * 0.328);
  vec2 satellite_position = rotate2d(0.26) * satellite_orbit - vec2(0.04, 0.02);
  float satellite_core = smoothstep(0.032, 0.005, length(p - satellite_position));
  float satellite_halo = smoothstep(0.075, 0.012, length(p - satellite_position)) * 0.22;

  float wave = body * (0.11 + broad_noise * 0.58 + detail_noise * 0.2);
  float intensity = wave + body_edge * 0.46 + contour + ring_a * 0.8 + ring_b + satellite_core + satellite_halo;
  float text_clear = smoothstep(0.2, 0.64, length(p * vec2(0.7, 1.42)));
  intensity *= 0.06 + text_clear * 0.94;
  intensity *= smoothstep(1.35, 0.68, length(p));
  intensity = clamp(intensity, 0.0, 1.0);

  float glyph_index = floor(intensity * (u_glyph_count - 1.0) + 0.001);
  vec2 glyph_uv = vec2((glyph_index + cell_uv.x) / u_glyph_count, 1.0 - cell_uv.y);
  float glyph = texture(u_glyphs, glyph_uv).r * smoothstep(0.035, 0.12, intensity);

  vec3 cyan = vec3(0.025, 0.714, 0.831);
  vec3 blue = vec3(0.145, 0.388, 0.922);
  vec3 violet = vec3(0.60, 0.27, 1.0);
  float color_flow = 0.5 + 0.5 * sin(time * 2.0 + p.x * 3.5 - p.y * 2.1 + broad_noise * 4.5);
  vec3 color = color_flow < 0.5
    ? mix(cyan, blue, color_flow * 2.0)
    : mix(blue, violet, (color_flow - 0.5) * 2.0);

  vec3 background = vec3(0.031, 0.043, 0.071);
  vec3 final_color = background + color * intensity * 0.045 + color * glyph * (0.58 + intensity * 0.48);
  out_color = vec4(final_color, 1.0);
}
`

function compileShader(gl: WebGL2RenderingContext, type: number, source: string) {
  const shader = gl.createShader(type)
  if (!shader) throw new Error('Unable to create shader')
  gl.shaderSource(shader, source)
  gl.compileShader(shader)
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const message = gl.getShaderInfoLog(shader) ?? 'Unknown shader compilation error'
    gl.deleteShader(shader)
    throw new Error(message)
  }
  return shader
}

function createProgram(gl: WebGL2RenderingContext) {
  const program = gl.createProgram()
  if (!program) throw new Error('Unable to create WebGL program')
  const vertex = compileShader(gl, gl.VERTEX_SHADER, VERTEX_SHADER)
  const fragment = compileShader(gl, gl.FRAGMENT_SHADER, FRAGMENT_SHADER)
  gl.attachShader(program, vertex)
  gl.attachShader(program, fragment)
  gl.linkProgram(program)
  gl.deleteShader(vertex)
  gl.deleteShader(fragment)
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const message = gl.getProgramInfoLog(program) ?? 'Unknown WebGL link error'
    gl.deleteProgram(program)
    throw new Error(message)
  }
  return program
}

function createGlyphTexture(gl: WebGL2RenderingContext) {
  const glyphs = ' .:+=*'
  const atlas = document.createElement('canvas')
  atlas.width = glyphs.length * 16
  atlas.height = 24
  const context = atlas.getContext('2d')
  if (!context) throw new Error('Unable to create glyph atlas')
  context.fillStyle = '#000'
  context.fillRect(0, 0, atlas.width, atlas.height)
  context.fillStyle = '#fff'
  context.font = '600 20px ui-monospace, SFMono-Regular, Menlo, monospace'
  context.textAlign = 'center'
  context.textBaseline = 'middle'
  glyphs.split('').forEach((glyph, index) => context.fillText(glyph, index * 16 + 8, 12))

  const texture = gl.createTexture()
  if (!texture) throw new Error('Unable to create glyph texture')
  gl.bindTexture(gl.TEXTURE_2D, texture)
  gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, 1)
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, atlas)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
  return { texture, count: glyphs.length }
}

export function OrbitalField({ paused }: { paused: boolean }) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const gl = canvas.getContext('webgl2', { alpha: false, antialias: false })
    if (!gl) {
      canvas.dataset.unsupported = 'true'
      return
    }
    const target: HTMLCanvasElement = canvas
    const context: WebGL2RenderingContext = gl
    const program = createProgram(context)
    const position = context.getAttribLocation(program, 'a_position')
    const resolution = context.getUniformLocation(program, 'u_resolution')
    const time = context.getUniformLocation(program, 'u_time')
    const glyphSampler = context.getUniformLocation(program, 'u_glyphs')
    const glyphCount = context.getUniformLocation(program, 'u_glyph_count')
    const buffer = context.createBuffer()
    const glyphAtlas = createGlyphTexture(context)
    const staticFrame = window.matchMedia('(prefers-reduced-motion: reduce)').matches || paused

    context.bindBuffer(context.ARRAY_BUFFER, buffer)
    context.bufferData(context.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), context.STATIC_DRAW)
    context.useProgram(program)
    context.enableVertexAttribArray(position)
    context.vertexAttribPointer(position, 2, context.FLOAT, false, 0, 0)
    context.activeTexture(context.TEXTURE0)
    context.bindTexture(context.TEXTURE_2D, glyphAtlas.texture)
    context.uniform1i(glyphSampler, 0)
    context.uniform1f(glyphCount, glyphAtlas.count)

    function resize() {
      const ratio = Math.min(window.devicePixelRatio || 1, 1.5)
      const width = Math.max(1, Math.floor(target.clientWidth * ratio))
      const height = Math.max(1, Math.floor(target.clientHeight * ratio))
      if (target.width !== width || target.height !== height) {
        target.width = width
        target.height = height
        context.viewport(0, 0, width, height)
      }
    }

    let frame = 0
    let startedAt = performance.now()
    function render(now: number) {
      resize()
      context.uniform2f(resolution, target.width, target.height)
      context.uniform1f(time, staticFrame ? 7 : (now - startedAt) / 1000)
      context.drawArrays(context.TRIANGLES, 0, 3)
      if (!staticFrame) frame = requestAnimationFrame(render)
    }

    function handleVisibility() {
      cancelAnimationFrame(frame)
      if (!document.hidden && !staticFrame) {
        startedAt = performance.now()
        frame = requestAnimationFrame(render)
      }
    }

    const observer = new ResizeObserver(resize)
    observer.observe(target)
    document.addEventListener('visibilitychange', handleVisibility)
    frame = requestAnimationFrame(render)
    return () => {
      observer.disconnect()
      document.removeEventListener('visibilitychange', handleVisibility)
      cancelAnimationFrame(frame)
      context.deleteTexture(glyphAtlas.texture)
      context.deleteBuffer(buffer)
      context.deleteProgram(program)
    }
  }, [paused])

  return <canvas ref={canvasRef} className="orbital-field" aria-label="Abstract ambient orbital field" />
}
