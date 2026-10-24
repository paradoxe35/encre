use std::env;
use std::path::PathBuf;

fn main() {
    let crate_dir = env::var("CARGO_MANIFEST_DIR").unwrap();
    let crate_path = PathBuf::from(&crate_dir);
    let output_file = crate_path.join("bindings.h");
    let config_file = crate_path.join("cbindgen.toml");

    let builder = if config_file.exists() {
        cbindgen::Builder::new().with_crate(&crate_dir).with_config(
            cbindgen::Config::from_file(&config_file).expect("Failed to read cbindgen.toml"),
        )
    } else {
        cbindgen::Builder::new()
            .with_crate(&crate_dir)
            .with_language(cbindgen::Language::C)
            .with_pragma_once(true)
            .with_include_guard("ENCRE_FFI_H")
            .with_documentation(true)
            .with_sys_include("stdint.h")
            .with_sys_include("stdbool.h")
            .with_tab_width(4)
    };

    builder
        .generate()
        .expect("Unable to generate C bindings with cbindgen")
        .write_to_file(output_file);

    // ggml is C++, and on MinGW nothing links its runtime into what cargo links itself, the tests;
    // the static library leaves that to the Go build, which adds -lstdc++. Linked from the archive,
    // as the Go build links statically too: the libstdc++ DLL first on PATH may not be the one built against.
    let target = env::var("TARGET").unwrap_or_default();
    if target.ends_with("-windows-gnu") {
        println!("cargo:rustc-link-arg=-l:libstdc++.a");
    }

    println!("cargo:rerun-if-changed=src/");
    println!("cargo:rerun-if-changed=cbindgen.toml");
}
