; The native bootstrapper passes a per-session log path. FileWrite logging works
; with the normal NSIS compiler; it does not require a debug NSIS distribution.
!macro OctopusSetupStage STAGE
  Push $0
  Push $1
  Push $2
  StrCpy $2 0
  IfErrors 0 +2
    StrCpy $2 1
  ClearErrors
  ${StdUtils.GetParameter} $0 "OCTOPUS_LOG" ""
  ${If} $0 != ""
    ClearErrors
    FileOpen $1 "$0" a
    ${IfNot} ${Errors}
      FileSeek $1 0 END
      FileWrite $1 "${STAGE}$\r$\n"
      FileClose $1
    ${EndIf}
  ${EndIf}
  ClearErrors
  ${If} $2 == 1
    SetErrors
  ${EndIf}
  Pop $2
  Pop $1
  Pop $0
!macroend

!macro preInit
  !insertmacro OctopusSetupStage "initializing"
!macroend

!macro customInit
  !insertmacro OctopusSetupStage "ready"
!macroend

!macro customInstall
  !insertmacro OctopusSetupStage "complete"
!macroend
