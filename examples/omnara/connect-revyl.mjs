#!/usr/bin/env node
// Signs in to Revyl's hosted MCP server in your browser and stores the result
// as an Omnara MCP OAuth secret, for a self-hosted Omnara: Revyl's sign-in
// accepts only Omnara Cloud's callback URL, so `npx omnara secrets mcp-oauth`
// fails there with invalid_redirect_uri.
//
//   node connect-revyl.mjs --project <omnara-project-id> [--name revyl-mcp-oauth]
//
// Needs Node 18+ and a logged-in Omnara CLI (`npx omnara whoami`). The tokens
// go straight from Revyl to Omnara and are never printed.

import { createHash, randomBytes } from 'node:crypto'
import { execFileSync, spawn } from 'node:child_process'
import { createServer } from 'node:http'
import { parseArgs } from 'node:util'

const MCP_URL = 'https://backend.revyl.ai/api/v1/mcp'
const PORT = 33418
const REDIRECT_URI = `http://localhost:${PORT}/callback`
const headers = { 'user-agent': 'revyl-omnara-connect/1.0', accept: 'application/json' }

const { values } = parseArgs({ options: { project: { type: 'string' }, name: { type: 'string', default: 'revyl-mcp-oauth' } } })
if (!values.project) throw new Error('pass --project <omnara-project-id>')

async function json(url, init = {}) {
  const response = await fetch(url, { ...init, headers: { ...headers, ...init.headers } })
  const body = await response.json()
  if (!response.ok) throw new Error(`${url}: ${response.status} ${body.error ?? ''} ${body.error_description ?? ''}`)
  return body
}

const resource = await json('https://backend.revyl.ai/.well-known/oauth-protected-resource/api/v1/mcp')
const issuer = new URL(resource.authorization_servers[0])
const server = await json(`${issuer.origin}/.well-known/oauth-authorization-server${issuer.pathname}`)
const client = await json(server.registration_endpoint, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({
    client_name: 'Omnara (Revyl connect)',
    redirect_uris: [REDIRECT_URI],
    grant_types: ['authorization_code', 'refresh_token'],
    response_types: ['code'],
    token_endpoint_auth_method: 'none',
  }),
})

const verifier = randomBytes(48).toString('base64url')
const state = randomBytes(16).toString('base64url')
const authorizeURL = new URL(server.authorization_endpoint)
authorizeURL.search = new URLSearchParams({
  response_type: 'code',
  client_id: client.client_id,
  redirect_uri: REDIRECT_URI,
  scope: resource.scopes_supported.join(' '),
  state,
  code_challenge: createHash('sha256').update(verifier).digest('base64url'),
  code_challenge_method: 'S256',
  resource: MCP_URL,
}).toString()

let finishSignIn
const signIn = new Promise((resolve, reject) => { finishSignIn = { resolve, reject } })
const handler = (request, response) => {
  const url = new URL(request.url, REDIRECT_URI)
  const params = url.searchParams
  if (url.pathname !== '/callback' || (!params.has('code') && !params.has('error'))) {
    return response.writeHead(404).end()
  }
  if (params.get('state') !== state || !params.get('code')) {
    response.writeHead(400).end('Revyl sign-in did not return a code. Run the script again.')
    return finishSignIn.reject(new Error(params.get('error_description') ?? params.get('error') ?? 'sign-in state did not match'))
  }
  response.writeHead(200).end('Revyl is connected to Omnara. You can close this tab.')
  finishSignIn.resolve(params.get('code'))
}
const listeners = await Promise.allSettled(['127.0.0.1', '::1'].map((host) => new Promise((resolve, reject) => {
  createServer(handler).once('error', reject).listen(PORT, host, resolve)
})))
if (!listeners.some(({ status }) => status === 'fulfilled')) {
  throw new Error(`port ${PORT} is in use: stop whatever is listening on it (another connect-revyl run?) and try again`)
}

console.log(`Open this link, sign in to Revyl, and choose the organization:\n\n${authorizeURL}\n`)
const opener = { darwin: 'open', win32: 'explorer' }[process.platform] ?? 'xdg-open'
spawn(opener, [authorizeURL.toString()], { stdio: 'ignore', detached: true }).on('error', () => {}).unref()
const code = await signIn

const tokens = await json(server.token_endpoint, {
  method: 'POST',
  headers: { 'content-type': 'application/x-www-form-urlencoded' },
  body: new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: REDIRECT_URI,
    client_id: client.client_id,
    code_verifier: verifier,
    resource: MCP_URL,
  }),
})

let output
try {
  output = execFileSync('npx', ['-y', 'omnara', 'secrets', 'create', '--owner-kind', 'project', '--owner-project-id', values.project,
    '--name', values.name, '--material-kind', 'oauth_token_set',
    '--material-access-token', tokens.access_token,
    '--material-access-token-expires-in-seconds', String(tokens.expires_in),
    '--material-refresh-refresh-token', tokens.refresh_token,
    '--material-refresh-token-endpoint', server.token_endpoint,
    '--material-refresh-client-id', client.client_id,
    '--material-refresh-resource', MCP_URL,
    '--material-mcp-url', MCP_URL,
    '--material-scopes', tokens.scope,
    '--material-token-type', tokens.token_type,
    '--json'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'], shell: process.platform === 'win32' })
} catch (error) {
  console.error(`npx omnara secrets create failed (exit ${error.status ?? error.code}); the Revyl tokens were not stored. Run the script again.`)
  process.exit(1)
}
const secret = JSON.parse(output)
console.log(`Created Omnara secret ${secret.name}: ${secret.id}`)
process.exit(0)
