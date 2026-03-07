# workspace-launcher fish integration

set -q __workspace_launcher_bin; or set -g __workspace_launcher_bin workspace-launcher

function workspace-launcher-cd --description 'Change directory with workspace-launcher'
    set -l query ''
    if status --is-interactive
        set query (commandline --current-token 2>/dev/null | string collect)
    end

    set -l result ($__workspace_launcher_bin $argv --query "$query")
    or return
    test -n "$result"; or return

    cd -- $result
end

function workspace-launcher-widget --description 'Open workspace-launcher and cd to selection'
    workspace-launcher-cd
    or begin
        commandline -f repaint
        return
    end

    commandline -r ''
    commandline -f repaint
end

bind \cg workspace-launcher-widget
bind -M insert \cg workspace-launcher-widget
