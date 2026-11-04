local Event = require("ui/event")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")

local Notes = WidgetContainer:extend{
    name = "yandexbooksnotes",
    is_doc_only = true,
}

function Notes:onReaderReady()
    if self.opened then return end
    local requested = os.getenv("YANDEX_BOOKS_ANNOTATIONS")
    local document = self.ui and self.ui.document
    if not document or not requested or requested == "" or document.file ~= requested then
        return
    end
    self.opened = true
    UIManager:nextTick(function()
        if self.ui.document == document then
            self.ui:handleEvent(Event:new("ShowBookmark"))
        end
    end)
end

return Notes
