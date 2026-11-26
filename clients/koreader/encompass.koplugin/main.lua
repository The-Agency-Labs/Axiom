--[[--
Encompass sync for KOReader.

Reads KOReader's own statistics database (statistics.sqlite3) and uploads new
page views to POST /api/v1/reading/koreader. The server groups them into
sessions, so this plugin only has to send raw rows; resending is harmless.

When it syncs:
  * on suspend (sleep cover / power button), on closing a book and on exit,
    if Wi-Fi is up;
  * on resume and whenever Wi-Fi connects, to flush anything missed.

It also sends the open book's table of contents (chapter titles and the
pages they start on, as KOReader lays the book out) so Encompass can show
"Chapter 12: Page 214". It's sent when it changes: a new book, or a new
layout after a font change.

Position sync is separate: use KOReader's built-in "Progress sync" pointed at
<server>/kosync with your username and the same token as the password.
--]]--

local DataStorage = require("datastorage")
local lfs = require("libs/libkoreader-lfs")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local logger = require("logger")
local ltn12 = require("ltn12")
local json = require("json")
local _ = require("gettext")
local T = require("ffi/util").template

local SETTINGS_KEY = "encompass"
-- Resend this much history each time, in case the statistics plugin flushed
-- rows after our last upload. The server ignores duplicates.
local OVERLAP_SECONDS = 6 * 3600
local MAX_ROWS_PER_REQUEST = 4000
local TIMEOUT_SECONDS = 8

local Encompass = WidgetContainer:extend{
    name = "encompass",
    is_doc_only = false,
}

-- Optional Tailscale helper (see clients/koreader/kindle/ts.sh). When it's
-- installed, Tailscale runs only while KOReader is open, which saves battery.
local TAILSCALE = "/mnt/us/tailscale/ts.sh"

local function tailscale(action, wait)
    if lfs.attributes(TAILSCALE, "mode") ~= "file" then return end
    os.execute(TAILSCALE .. " " .. action .. " >/dev/null 2>&1" .. (wait and "" or " &"))
end

function Encompass:init()
    self.settings = G_reader_settings:readSetting(SETTINGS_KEY) or {}
    self.ui.menu:registerToMainMenu(self)
    if self.settings.tailscale ~= false then
        tailscale("start")  -- no-op if it's already running
    end
end

-- The file browser and the reader each have their own instance of this
-- plugin; re-read the shared settings so neither overwrites the other.
function Encompass:reload()
    self.settings = G_reader_settings:readSetting(SETTINGS_KEY) or self.settings
end

function Encompass:save()
    G_reader_settings:saveSetting(SETTINGS_KEY, self.settings)
end

function Encompass:isConfigured()
    return self.settings.server and self.settings.server ~= ""
        and self.settings.token and self.settings.token ~= ""
end

local function isOnline()
    local ok, NetworkMgr = pcall(require, "ui/network/manager")
    if not ok then return false end
    if NetworkMgr.isOnline then return NetworkMgr:isOnline() end
    if NetworkMgr.isConnected then return NetworkMgr:isConnected() end
    return false
end

-- Make sure the statistics plugin has written its in-memory page views.
function Encompass:flushStatistics()
    local stats = self.ui and self.ui.statistics
    if stats and stats.insertDB then
        local ok, err = pcall(stats.insertDB, stats)
        if not ok then logger.warn("encompass: statistics flush failed", err) end
    end
end

function Encompass:readRows(since)
    local SQ3 = require("lua-ljsqlite3/init")
    local db_path = DataStorage:getSettingsDir() .. "/statistics.sqlite3"
    local ok, conn = pcall(SQ3.open, db_path)
    if not ok or not conn then
        logger.warn("encompass: cannot open", db_path)
        return nil
    end

    local books = {}
    local order = {}
    local max_time = since
    local count = 0
    local stmt = conn:prepare([[
        SELECT b.md5, b.title, b.authors, b.pages,
               p.page, p.start_time, p.duration, p.total_pages
        FROM page_stat_data p JOIN book b ON b.id = p.id_book
        WHERE p.start_time > ? AND b.md5 IS NOT NULL
        ORDER BY p.start_time
        LIMIT ?;
    ]])
    stmt:bind(since, MAX_ROWS_PER_REQUEST)
    local row = stmt:step()
    while row do
        local md5 = tostring(row[1])
        local book = books[md5]
        if not book then
            book = {
                md5 = md5,
                title = row[2] and tostring(row[2]) or "",
                authors = row[3] and tostring(row[3]) or "",
                total_pages = tonumber(row[4]),
                page_stats = {},
            }
            books[md5] = book
            table.insert(order, book)
        end
        local start_time = tonumber(row[6])
        table.insert(book.page_stats, {
            page = tonumber(row[5]),
            start_time = start_time,
            duration = tonumber(row[7]),
            total_pages = tonumber(row[8]),
        })
        if start_time > max_time then max_time = start_time end
        count = count + 1
        row = stmt:step()
    end
    stmt:close()
    conn:close()
    return order, max_time, count
end

function Encompass:post(path, body)
    local url = self.settings.server:gsub("/+$", "") .. path
    local http = url:match("^https://") and require("ssl.https") or require("socket.http")
    local socketutil = require("socketutil")
    socketutil:set_timeout(TIMEOUT_SECONDS, TIMEOUT_SECONDS * 2)
    local response = {}
    local _ok, code = http.request{
        url = url,
        method = "POST",
        headers = {
            ["Authorization"] = "Bearer " .. self.settings.token,
            ["Content-Type"] = "application/json",
            ["Content-Length"] = tostring(#body),
        },
        source = ltn12.source.string(body),
        sink = ltn12.sink.table(response),
    }
    socketutil:reset_timeout()
    return tonumber(code), table.concat(response)
end

-- The open book's table of contents, ready to send, or nil (no book open,
-- no TOC). `key` changes when the chapters or the layout change.
function Encompass:currentTOC()
    local ui = self.ui
    if not (ui and ui.document and ui.toc) then return nil end
    local ok = pcall(ui.toc.fillToc, ui.toc)
    local toc = ok and ui.toc.toc
    if not toc or #toc == 0 then return nil end
    local md5 = ui.doc_settings and ui.doc_settings:readSetting("partial_md5_checksum")
    if not md5 then
        local util = require("util")
        md5 = util.partialMD5 and util.partialMD5(ui.document.file)
    end
    local pages = ui.document:getPageCount()
    if not md5 or not pages or pages <= 0 then return nil end
    local entries = {}
    for _, t in ipairs(toc) do
        if t.title and tonumber(t.page) then
            table.insert(entries, { title = t.title, page = tonumber(t.page), depth = tonumber(t.depth) or 1 })
        end
    end
    if #entries == 0 then return nil end
    local props = ui.doc_props or {}
    return {
        md5 = md5,
        key = pages .. ":" .. #entries .. ":" .. entries[#entries].page,
        payload = {
            md5 = md5,
            title = props.display_title or props.title or "",
            authors = props.authors or "",
            total_pages = pages,
            toc = entries,
        },
    }
end

-- Remember the open book's table of contents until it has been sent, so a
-- later sync from the file browser (no book open) still sends it.
function Encompass:captureTOC()
    if not (self.ui and self.ui.document) then return end
    local ok, toc = pcall(self.currentTOC, self)
    if not ok then
        logger.warn("encompass: reading table of contents failed", toc)
        self:reload()
        self.settings.toc_status = T(_("Chapters: error %1"), tostring(toc):sub(1, 60))
        self:save()
        return
    end
    if not toc then return end
    self:reload()
    self.settings.toc_sent = self.settings.toc_sent or {}
    if self.settings.toc_sent[toc.md5] == toc.key then return end
    self.settings.toc_pending = self.settings.toc_pending or {}
    self.settings.toc_pending[toc.md5] = { key = toc.key, payload = toc.payload }
    self:save()
end

-- Send every table of contents captured but not yet accepted by Encompass.
function Encompass:sendTOCs()
    self:captureTOC()
    local pending = self.settings.toc_pending or {}
    self.settings.toc_sent = self.settings.toc_sent or {}
    for md5, item in pairs(pending) do
        local okp, code, body = pcall(self.post, self, "/api/v1/reading/koreader/toc", json.encode(item.payload))
        if okp and code == 200 then
            pending[md5] = nil
            self.settings.toc_sent[md5] = item.key
            self.settings.toc_status = T(_("Chapters: sent %1 for %2 at %3"), #item.payload.toc,
                (item.payload.title or ""):sub(1, 24), os.date("%H:%M"))
            logger.info("encompass: sent table of contents", md5, #item.payload.toc)
        else
            local why = okp and (tostring(code) .. " " .. tostring(body):sub(1, 60)) or tostring(code)
            self.settings.toc_status = T(_("Chapters: failed %1"), why)
            logger.warn("encompass: table of contents upload failed", why)
        end
    end
    self.settings.toc_pending = pending
    self:save()
end

-- Upload everything newer than the last successful sync. Returns rows sent.
function Encompass:sync(reason, interactive)
    if self.syncing then return 0 end
    if not self:isConfigured() then
        if interactive then
            UIManager:show(InfoMessage:new{ text = _("Set the server and token first.") })
        end
        return 0
    end
    if not isOnline() then
        logger.dbg("encompass: offline, will retry later (" .. reason .. ")")
        if interactive then
            UIManager:show(InfoMessage:new{ text = _("Not connected to Wi-Fi.") })
        end
        return 0
    end

    self.syncing = true
    self:reload()
    self:flushStatistics()
    local total = 0
    local watermark = tonumber(self.settings.watermark) or 0
    local since = math.max(0, watermark - OVERLAP_SECONDS)
    local device = self.settings.device_name or "KOReader"

    while true do
        local books, max_time, count = self:readRows(since)
        if not books or count == 0 then break end

        local ok, code, body = pcall(self.post, self, "/api/v1/reading/koreader",
            json.encode({ device = device, books = books }))
        if not ok or code ~= 200 then
            logger.warn("encompass: upload failed", code, body)
            self.settings.last_error = T(_("HTTP %1 at %2"), tostring(code), os.date("%Y-%m-%d %H:%M"))
            self:save()
            if interactive then
                UIManager:show(InfoMessage:new{ text = T(_("Sync failed: %1"), tostring(code)) })
            end
            self.syncing = false
            return total
        end

        total = total + count
        watermark = math.max(watermark, max_time)
        since = max_time
        if count < MAX_ROWS_PER_REQUEST then break end
    end

    self:sendTOCs()

    self.settings.watermark = watermark
    self.settings.last_sync = os.date("%Y-%m-%d %H:%M")
    self.settings.last_error = nil
    self:save()
    self.syncing = false
    logger.info("encompass: synced", total, "rows (" .. reason .. ")")
    if interactive then
        UIManager:show(InfoMessage:new{ text = T(_("Synced %1 page views."), total) })
    end
    return total
end

-- Events --------------------------------------------------------------------

function Encompass:onSuspend() self:sync("suspend") end
function Encompass:onReaderReady()
    -- A book was opened: keep its chapters, send them soon (layout settled).
    UIManager:scheduleIn(5, function()
        self:captureTOC()
        self:sync("open")
    end)
end
function Encompass:onCloseDocument()
    self:captureTOC() -- even offline: it waits for the next sync
    self:sync("close")
end
function Encompass:onExit()
    self:sync("exit")
    if self.settings.tailscale ~= false then
        tailscale("stop", true)
    end
end
function Encompass:onResume()
    -- Wi-Fi usually needs a few seconds after wake.
    UIManager:scheduleIn(15, function() self:sync("resume") end)
end
function Encompass:onNetworkConnected()
    UIManager:scheduleIn(2, function() self:sync("network") end)
end

-- Menu ----------------------------------------------------------------------

function Encompass:editSetting(key, title, hint)
    local dialog
    dialog = InputDialog:new{
        title = title,
        input = self.settings[key] or "",
        input_hint = hint,
        buttons = {{
            { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
            { text = _("Save"), is_enter_default = true, callback = function()
                self.settings[key] = dialog:getInputText()
                self:save()
                UIManager:close(dialog)
            end },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Encompass:addToMainMenu(menu_items)
    menu_items.encompass = {
        text = _("Encompass sync"),
        sorting_hint = "tools",
        sub_item_table = {
            {
                text = _("Sync now"),
                callback = function() self:sync("manual", true) end,
            },
            {
                text = _("Server URL"),
                keep_menu_open = true,
                callback = function()
                    self:editSetting("server", _("Server URL"), "https://encompass.your-tailnet.ts.net")
                end,
            },
            {
                text = _("API token (write)"),
                keep_menu_open = true,
                callback = function()
                    self:editSetting("token", _("API token"), "enc_…")
                end,
            },
            {
                text = _("Device name"),
                keep_menu_open = true,
                callback = function()
                    self:editSetting("device_name", _("Device name"), "Kindle")
                end,
            },
            {
                text_func = function()
                    if self.settings.last_error then
                        return T(_("Last error: %1"), self.settings.last_error)
                    end
                    return T(_("Last sync: %1"), self.settings.last_sync or _("never"))
                end,
                enabled_func = function() return false end,
            },
            {
                text_func = function()
                    return self.settings.toc_status or _("Chapters: not sent yet (open a book)")
                end,
                enabled_func = function() return false end,
            },
            {
                text = _("Run Tailscale while KOReader is open"),
                enabled_func = function()
                    return lfs.attributes(TAILSCALE, "mode") == "file"
                end,
                checked_func = function() return self.settings.tailscale ~= false end,
                callback = function()
                    self.settings.tailscale = self.settings.tailscale == false
                    self:save()
                    tailscale(self.settings.tailscale and "start" or "stop")
                end,
            },
            {
                text = _("Resend all history"),
                callback = function()
                    self.settings.watermark = 0
                    self:save()
                    self:sync("full", true)
                end,
            },
        },
    }
end

return Encompass
