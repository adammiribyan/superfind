local T = "com.superlogical.terminal"
local panes = {}
for _, s in ipairs(rex.call("session.list", {}).sessions) do
  rex.session.attach{session_id = s.session_id}
  local listed = rex.session.list_blocks{session_id = s.session_id}
  for _, b in ipairs(listed and listed.blocks or {}) do
    if b.creator_name == T then
      local where = {session_id = s.session_id, block_id = b.block_id}
      local p = rex.block.call(T, "process", where)
      local st = rex.block.call(T, "program_status", where)
      panes[#panes+1] = {session = s.session_id, window = b.window_id, block = b.block_id,
        pid = p and p.foreground and p.foreground.pid, records = st and st.records}
    end
  end
end
return {panes = panes}
