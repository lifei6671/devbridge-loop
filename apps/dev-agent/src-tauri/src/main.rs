#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    dev_agent_lib::run();
}
