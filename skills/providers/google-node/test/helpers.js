const fs = require('fs')
const os = require('os')
const path = require('path')
const Module = require('module')
const { Readable } = require('stream')

class ExitError extends Error {
  constructor(code) {
    super(`process exited with code ${code}`)
    this.code = code
  }
}

function makeTempHome(globalConfig = {}) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'google-node-test-'))
  fs.writeFileSync(
    path.join(home, 'global.json'),
    JSON.stringify({
      GMAIL_CLIENT_ID: 'client-id',
      GMAIL_CLIENT_SECRET: 'client-secret',
      GMAIL_REFRESH_TOKEN: 'refresh-token',
      ...globalConfig,
    }),
  )
  return home
}

function withMockedLoad(mocks, fn) {
  const originalLoad = Module._load
  Module._load = function mockedLoad(request, parent, isMain) {
    if (Object.prototype.hasOwnProperty.call(mocks, request)) {
      return mocks[request]
    }
    return originalLoad.apply(this, arguments)
  }

  try {
    return fn()
  } finally {
    Module._load = originalLoad
  }
}

function loadFresh(modulePath, { home, googleapis } = {}) {
  const resolved = require.resolve(modulePath)
  const originalHome = process.env.HOME

  delete require.cache[resolved]
  if (home) {
    delete require.cache[path.join(home, 'global.json')]
    process.env.HOME = home
  }

  try {
    if (googleapis) {
      return withMockedLoad({ googleapis }, () => require(resolved))
    }
    return require(resolved)
  } finally {
    if (home) {
      process.env.HOME = originalHome
    }
  }
}

function createGoogleapisMock(services = {}) {
  class OAuth2 {
    constructor(clientId, clientSecret) {
      this.clientId = clientId
      this.clientSecret = clientSecret
      this.credentials = null
    }

    setCredentials(credentials) {
      this.credentials = credentials
    }
  }

  const calls = {
    gmail: [],
    drive: [],
    sheets: [],
    calendar: [],
  }

  return {
    google: {
      auth: { OAuth2 },
      gmail(args) {
        calls.gmail.push(args)
        return services.gmail
      },
      drive(args) {
        calls.drive.push(args)
        return services.drive
      },
      sheets(args) {
        calls.sheets.push(args)
        return services.sheets
      },
      calendar(args) {
        calls.calendar.push(args)
        return services.calendar
      },
    },
    calls,
    OAuth2,
  }
}

function makeStdin(text = '', isTTY = true) {
  const stream = Readable.from(text ? [text] : [])
  stream.isTTY = isTTY
  return stream
}

async function runCli(main, argv, overrides = {}) {
  const stdoutLines = []
  const stderrLines = []

  let exitCode = 0
  const deps = {
    env: overrides.env || process.env,
    fs: overrides.fs,
    lib: overrides.lib,
    stdin: overrides.stdin || makeStdin('', true),
    print: line => stdoutLines.push(String(line)),
    error: line => stderrLines.push(String(line)),
    sleep: overrides.sleep || (async () => {}),
    now: overrides.now || (() => 0),
    ...overrides,
  }

  deps.exit = overrides.exit || (code => {
    exitCode = code
    throw new ExitError(code)
  })

  try {
    await main(argv, deps)
  } catch (error) {
    if (!(error instanceof ExitError)) {
      throw error
    }
  }

  return {
    exitCode,
    stdout: stdoutLines.join('\n'),
    stderr: stderrLines.join('\n'),
    stdoutLines,
    stderrLines,
  }
}

module.exports = {
  createGoogleapisMock,
  ExitError,
  loadFresh,
  makeStdin,
  makeTempHome,
  runCli,
}
