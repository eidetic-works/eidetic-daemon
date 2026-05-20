# bash completion for eideticd (v0.0.48+)
#
# Install:
#   1. macOS (Homebrew):   brew install bash-completion@2; cp eideticd.bash $(brew --prefix)/etc/bash_completion.d/eideticd
#   2. Linux:              sudo cp eideticd.bash /etc/bash_completion.d/eideticd
#   3. Sourced manually:   source /path/to/eideticd.bash (add to ~/.bashrc)
#
# Homebrew formula automatically installs this to the bash-completion dir on
# `brew install eideticd` (v0.0.48+ formula).

_eideticd() {
    local cur prev words cword
    _init_completion 2>/dev/null || {
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    }

    # Top-level flags that take no arg
    local zero_arg_flags="
        -version --version
        -stats --stats
        -check --check
        -backups --backups
        -sync-now --sync-now
        -restore --restore
        -install --install
        -uninstall --uninstall
        -init --init
        -yes --yes
        -purge --purge
        -auth --auth
        -help --help -h
    "
    # Flags that take a value
    local value_flags="
        -uds --uds
        -tcp --tcp
        -bridge --bridge
    "

    case "$prev" in
        -uds|--uds)
            # Suggest common UDS paths
            COMPREPLY=( $(compgen -W "/tmp/eidetic-daemon.sock /var/run/eidetic.sock" -- "$cur") )
            return 0
            ;;
        -tcp|--tcp)
            COMPREPLY=( $(compgen -W "127.0.0.1:9876 0.0.0.0:9876" -- "$cur") )
            return 0
            ;;
        -bridge|--bridge)
            COMPREPLY=( $(compgen -W ":8421 127.0.0.1:8421" -- "$cur") )
            return 0
            ;;
    esac

    COMPREPLY=( $(compgen -W "$zero_arg_flags $value_flags" -- "$cur") )
    return 0
}

complete -F _eideticd eideticd
