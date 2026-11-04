-- Set the requested target language once. Future language choices stay in KOReader.
if G_reader_settings and not G_reader_settings:isTrue("reading_kit_target_language_set") then
    G_reader_settings:saveSetting("translator_to_language", "ru")
    G_reader_settings:saveSetting("reading_kit_target_language_set", true)
end
